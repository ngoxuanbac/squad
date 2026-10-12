package core

import (
	"context"
	"os"
	"strings"
	"testing"
)

func noticesOf(e *Engine, kind string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, n := range e.notices(50) {
		if n.Kind == kind {
			out = append(out, n.Body)
		}
	}
	return out
}

func teamOf(t *testing.T, e *Engine, name string) *Team {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	tm, err := e.tWhere(`name=? ORDER BY created_at DESC LIMIT 1 --`, name)
	if err != nil || tm == nil {
		t.Fatalf("no team %s: %v", name, err)
	}
	return tm
}

func pOf(t *testing.T, e *Engine, name string) *Participant {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	p, _ := e.pWhere(`name=?`, name)
	if p == nil {
		t.Fatalf("no participant %s", name)
	}
	return p
}

func TestAdmit(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	root := t.TempDir()
	lead := must(e.RegisterSession(root, "", "", "claude"))
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "lead-peer", Name: "api"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "w1", Task: "t"}))
	here := must(e.RegisterSession(root, "", "", "claude"))
	away := must(e.RegisterSession(t.TempDir(), "", "", "claude"))

	cases := []struct {
		token string
		args  AgentArgs
		rule  string
	}{
		{lead.Token, AgentArgs{Action: "admit", Name: here.Name}, ""},               // no role
		{lead.Token, AgentArgs{Action: "admit", Name: here.Name, Role: "boss"}, ""}, // not a role
		{lead.Token, AgentArgs{Action: "admit", Name: "nobody", Role: "peer"}, "admit.not_solo"},
		{lead.Token, AgentArgs{Action: "admit", Name: away.Name, Role: "peer"}, "admit.not_at_root"},
		{lead.Token, AgentArgs{Action: "admit", Name: here.Name, Role: "lead"}, "can_spawn"},
		{away.Token, AgentArgs{Action: "admit", Name: here.Name, Role: "peer"}, ""}, // a solo admits nobody
	}
	for _, c := range cases {
		_, err := e.Agent(c.token, c.args)
		if c.rule != "" {
			refusedRule(t, err, c.rule)
		} else if err == nil {
			t.Fatalf("%+v accepted", c.args)
		}
	}
	// A worker does not admit (it has no agent tool here; the rule holds for any worker).
	e.mu.Lock()
	w, _ := e.pWhere(`name=?`, "w1")
	own, _ := e.team(w.TeamID)
	_, err := e.admit(w, own, AgentArgs{Name: here.Name, Role: "peer"})
	e.mu.Unlock()
	refusedRule(t, err, "admit.headless")

	out := must(e.Agent(lead.Token, AgentArgs{Action: "admit", Name: here.Name, Role: "peer"}))
	if !strings.Contains(out, "admitted "+here.Name+" as peer") {
		t.Fatal(out)
	}
	h := pOf(t, e, here.Name)
	if h.Role != "peer" || h.SpawnedBy != lead.ID || h.TeamID == "" {
		t.Fatalf("admitted: %+v", h)
	}
	// It is a member now: routing applies to it.
	must(e.Send(here.Token, SendArgs{To: lead.Name, Body: "hi lead"}))
}

// A member that founds another team leaves its team: its mail and workers go to the gate; the
// gate that leaves passes the gate on; a team left without a live gate gets a gate_lost notice.
func TestFoundLeavesTheOldTeam(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	root := t.TempDir()
	gate := must(e.RegisterSession(root, "", "", "claude"))
	must(e.Agent(gate.Token, AgentArgs{Action: "found", Template: "p2p", Name: "crew"}))
	mem := must(e.RegisterSession(root, "", "", "claude"))
	must(e.Agent(gate.Token, AgentArgs{Action: "admit", Name: mem.Name, Role: "peer"}))
	must(e.Agent(mem.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "w1", Task: "t"}))
	must(e.Agent(gate.Token, AgentArgs{Action: "spawn", Template: "advisor", Name: "adv", Task: "?"}))
	must(e.Send(tokenOf(t, e, "w1"), SendArgs{To: mem.Name, Body: "for mem"}))
	e.mu.Lock() // the worker's mail stays unread by mem
	_, _ = e.db.Exec(`UPDATE mail SET acked_at='' WHERE to_id=?`, mem.ID)
	e.mu.Unlock()

	must(e.Agent(mem.Token, AgentArgs{Action: "found", Template: "lead-peer", Name: "mine"}))
	if m := pOf(t, e, mem.Name); m.TeamID != teamOf(t, e, "mine").ID || m.Role != "lead" {
		t.Fatalf("founder: %+v", m)
	}
	if w := pOf(t, e, "w1"); w.SpawnedBy != gate.ID {
		t.Fatalf("w1 reports to %s, want the gate", w.SpawnedBy)
	}
	msgs := must(e.Inbox(context.Background(), gate.Token, 0))
	if len(msgs) != 1 || msgs[0].Body != "for mem" {
		t.Fatalf("gate's inbox: %+v", msgs)
	}

	// The gate leaves: the next member (a session before workers) takes the gate.
	g2 := must(e.RegisterSession(root, "", "", "claude"))
	must(e.Agent(gate.Token, AgentArgs{Action: "admit", Name: g2.Name, Role: "peer"}))
	must(e.Agent(gate.Token, AgentArgs{Action: "found", Template: "p2p", Name: "next"}))
	if crew := teamOf(t, e, "crew"); crew.GateID != g2.ID {
		t.Fatalf("crew's gate %s, want %s", crew.GateID, g2.ID)
	}
	if w := pOf(t, e, "w1"); w.SpawnedBy != g2.ID {
		t.Fatal("w1 did not move to the new gate")
	}
	if teamOf(t, e, "adv").Status != TeamClosed {
		t.Fatal("the leaving gate's taskforce is open")
	}
	// The new gate goes away and leaves too: the next gate (w1) is a worker; a later leaver while
	// the gate is gone gets a gate_lost notice.
	must(e.Agent(g2.Token, AgentArgs{Action: "found", Template: "p2p", Name: "third"}))
	if crew := teamOf(t, e, "crew"); crew.GateID != mustID(e, "w1") {
		t.Fatalf("crew's gate is %q", crew.GateID)
	}
	e.mu.Lock()
	crew, _ := e.tWhere(`name=?`, "crew")
	w1, _ := e.byID(crew.GateID)
	_, _ = e.db.Exec(`UPDATE participants SET status=? WHERE id=?`, StatusGone, w1.ID)
	w1.Status = StatusGone
	x := &Participant{ID: "px", Name: "x"}
	e.leave(x, crew)
	// And with no member at all left to be the gate.
	solo := &Participant{ID: "py", Name: "y"}
	_, _ = e.db.Exec(`UPDATE participants SET team_id='' WHERE team_id=?`, crew.ID)
	_, _ = e.db.Exec(`UPDATE teams SET gate_id=? WHERE id=?`, solo.ID, crew.ID)
	crew.GateID = solo.ID
	e.leave(solo, crew)
	e.mu.Unlock()
	lost := noticesOf(e, "gate_lost")
	if len(lost) != 2 || !strings.Contains(lost[1], "its gate w1 is not live") || !strings.Contains(lost[0], "which has no live member now") {
		t.Fatalf("gate_lost: %q", lost)
	}
}

func TestReopenRules(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	root := t.TempDir()
	old := must(e.RegisterSession(root, "", "", "claude"))
	must(e.Agent(old.Token, AgentArgs{Action: "found", Template: "lead-peer", Name: "api"}))
	must(e.Agent(old.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "w1", Task: "t"}))
	must(e.Agent(old.Token, AgentArgs{Action: "spawn", Template: "advisor", Name: "adv", Task: "?"}))
	e.mu.Lock() // mail for the old gate from its team, unread when the team closed
	tm, _ := e.tWhere(`name=?`, "api")
	_, _ = e.db.Exec(`INSERT INTO mail(team_id,from_id,to_id,body,created_at) VALUES(?,?,?,?,?)`, tm.ID, mustID(e, "w1"), old.ID, "unread", now())
	e.mu.Unlock()
	must(e.Agent(old.Token, AgentArgs{Action: "close"}))

	other := must(e.RegisterSession(root, "", "", "claude"))
	away := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	_, err := e.Agent(other.Token, AgentArgs{Action: "reopen"})
	refused(t, err, "invalid")
	_, err = e.Agent(other.Token, AgentArgs{Action: "reopen", Team: "nope"})
	refused(t, err, "not_found")
	_, err = e.Agent(away.Token, AgentArgs{Action: "reopen", Team: "api"})
	refusedRule(t, err, "reopen.not_at_root")
	_, err = e.Agent(other.Token, AgentArgs{Action: "reopen", Team: "adv"})
	refused(t, err, "invalid") // a taskforce

	out := must(e.Agent(other.Token, AgentArgs{Action: "reopen", Team: "api"}))
	for _, want := range []string{"Reopened team api (template lead-peer); you are " + other.Name + " (lead), its gate.",
		"w1 (peer, worker): stopped", "Board: 0 pin(s)", "Unacked mail to you: 1", "Workers are stopped"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
	api := teamOf(t, e, "api")
	if api.Status != TeamOpen || api.GateID != other.ID || pOf(t, e, "w1").SpawnedBy != other.ID || pOf(t, e, "w1").Status != StatusStopped {
		t.Fatalf("reopened: %+v", api)
	}
	_, err = e.Agent(other.Token, AgentArgs{Action: "reopen", Team: "api"})
	refusedRule(t, err, "reopen.not_solo")
	_, err = e.Agent(old.Token, AgentArgs{Action: "reopen", Team: "api"})
	refused(t, err, "invalid") // open
	// The workers stay stopped until the gate resumes them.
	must(e.Agent(other.Token, AgentArgs{Action: "resume", Name: "w1"}))
	if pOf(t, e, "w1").Status == StatusStopped {
		t.Fatal("w1 not resumed")
	}
}

func TestCloseRules(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	root := t.TempDir()
	lead := must(e.RegisterSession(root, "", "", "claude"))
	_, err := e.Agent(lead.Token, AgentArgs{Action: "close"})
	refused(t, err, "invalid") // solo
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "p2p", Name: "crew"}))
	mem := must(e.RegisterSession(root, "", "", "claude"))
	must(e.Agent(lead.Token, AgentArgs{Action: "admit", Name: mem.Name, Role: "peer"}))
	_, err = e.Agent(mem.Token, AgentArgs{Action: "close"})
	refusedRule(t, err, "team_gate.close_not_gate")
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Template: "dual-lens", Name: "dl", Task: "?"}))
	_, err = e.Agent(tokenOf(t, e, "dl-chair"), AgentArgs{Action: "close"})
	refusedRule(t, err, "taskforce.close_by_caller")
	_, err = e.Agent(mem.Token, AgentArgs{Action: "close", Team: "dl"})
	refusedRule(t, err, "taskforce.not_yours")
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "w", Task: "t"}))
	e.mu.Lock()
	w, _ := e.pWhere(`name=?`, "w")
	own, _ := e.team(w.TeamID)
	_, err = e.closeFor(w, own, "")
	e.mu.Unlock()
	refusedRule(t, err, "team_gate.close_headless")
}

func TestAgentResumeAndTail(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	lead, p1, _ := leadPeer(t, e)
	must(e.Kill("p1"))
	eventually(t, e, "p1 stopped", func() bool { return e.runs[mustID(e, "p1")] == nil })
	_, err := e.Agent(p1, AgentArgs{Action: "resume", Name: "p2"})
	refused(t, err, "permission") // peers have no agent tool
	other := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	must(e.Agent(other.Token, AgentArgs{Action: "found", Template: "lead-peer"}))
	_, err = e.Agent(other.Token, AgentArgs{Action: "resume", Name: "p1"})
	refusedRule(t, err, "worker.not_yours")
	_, err = e.Agent(lead.Token, AgentArgs{Action: "resume", Name: "nobody"})
	refused(t, err, "not_found")
	if out := must(e.Agent(lead.Token, AgentArgs{Action: "resume", Name: "p1"})); out != "resumed p1" {
		t.Fatal(out)
	}
	if out := must(e.Agent(lead.Token, AgentArgs{Action: "resume", Name: "p1"})); !strings.Contains(out, "already running") {
		t.Fatal(out)
	}
	if out := must(e.Agent(lead.Token, AgentArgs{Action: "tail", Name: "p1"})); !strings.Contains(out, "no log yet") {
		t.Fatal(out)
	}
	if err := os.WriteFile(WorkerLog(e.dir, mustID(e, "p1")),
		[]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test"}}]}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := must(e.Agent(lead.Token, AgentArgs{Action: "tail", Name: "p1"})); !strings.Contains(out, "tool: Bash  go test") {
		t.Fatal(out)
	}
}

// A worker resumed more often than max_respawn_per_hour is parked, its lead told once; mail does
// not resume it; it still counts as live.
func TestRespawnParks(t *testing.T) {
	claude := &memRuntime{name: "claude"}
	e := memEngine(t, claude)
	writeTemplate(t, e, "once", `template: once
auto_join_role: lead
roles:
  lead: {tools: [send, inbox, agent], can_spawn: [peer]}
  peer: {tools: [send]}
routing:
  - {from: lead, to: peer}
  - {from: peer, to: lead}
limits: {depth: 1, concurrency: 1, max_respawn_per_hour: 1}
`)
	lead := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "once"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "w", Task: "t"}))
	stop := func() {
		must(e.Kill("w"))
		eventually(t, e, "w stopped", func() bool { return e.runs[mustID(e, "w")] == nil })
	}
	stop()
	must(e.Resume("w")) // the one resume an hour allows
	stop()
	_, err := e.Resume("w")
	refusedRule(t, err, "limits.max_respawn_per_hour")
	if pOf(t, e, "w").Status != StatusParked {
		t.Fatal("not parked")
	}
	note := lastMailTo(t, e, lead.Name)
	if note.FromID != EngineID || !strings.Contains(note.Body, "w resumed 1 times in the last hour; parked") {
		t.Fatalf("lead's note: %+v", note)
	}
	_, err = e.Resume("w")
	refusedRule(t, err, "limits.max_respawn_per_hour")
	starts := claude.count()
	must(e.Send(lead.Token, SendArgs{To: "w", Body: "wake up"}))
	if claude.count() != starts {
		t.Fatal("mail resumed a parked worker")
	}
	// Parked still counts against concurrency 1.
	_, err = e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "w2", Task: "t"})
	refusedRule(t, err, "limits.concurrency")
}
