package core

import (
	"strings"
	"testing"
	"time"
)

func ago(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339) }

// setWorking makes name a member working since `since` (state_since), its last turn ended at end.
func setWorking(e *Engine, name, since, end string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _ = e.db.Exec(`UPDATE participants SET status=? WHERE name=?`, StatusWorking, name)
	_, _ = e.db.Exec(`UPDATE participants SET state_since=?, last_turn_end=? WHERE name=?`, since, end, name)
}

func engineMailTo(e *Engine, id, kind string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	rows, _ := e.db.Query(`SELECT body FROM mail WHERE from_id=? AND to_id=? AND kind=? ORDER BY id`, EngineID, id, kind)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var b string
		_ = rows.Scan(&b)
		out = append(out, b)
	}
	return out
}

// lead-peer's timer: a peer working for more than 20m with no turn end is reported to its lead,
// once per working stretch.
func TestWatchTimer(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	lead, _, _ := leadPeer(t, e)
	stretch := ago(25 * time.Minute)
	setWorking(e, "p1", stretch, "")
	setWorking(e, "p2", ago(5*time.Minute), "") // not silent long enough
	e.Tick()
	e.Tick()
	got := engineMailTo(e, lead.ID, "watch")
	if len(got) != 1 || !strings.Contains(got[0], "p1 (peer, reports to you) has been working for 25m") ||
		!strings.Contains(got[0], "(rule silent_for 20m)") {
		t.Fatalf("watch mail: %q", got)
	}
	// A turn end inside the stretch restarts the silence clock, not the incident.
	setWorking(e, "p1", stretch, ago(time.Minute))
	e.Tick()
	if n := len(engineMailTo(e, lead.ID, "watch")); n != 1 {
		t.Fatalf("%d watch mails", n)
	}
	// A new working stretch is a new incident.
	e.mu.Lock()
	_, _ = e.db.Exec(`UPDATE participants SET status=? WHERE name=?`, StatusLive, "p1")
	e.mu.Unlock()
	setWorking(e, "p1", ago(21*time.Minute), "")
	e.Tick()
	if n := len(engineMailTo(e, lead.ID, "watch")); n != 2 {
		t.Fatalf("%d watch mails after a new stretch", n)
	}
}

// A role target is every member of that role but the subject.
func TestWatchTimerRoleTarget(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	writeTemplate(t, e, "w", `template: w
auto_join_role: lead
roles:
  lead: {tools: [send, agent], can_spawn: [peer]}
  peer: {tools: [send]}
routing:
  - {from: lead, to: peer}
timers:
  - {on: peer, silent_for: 1m, notify: peer}
limits: {depth: 1, concurrency: 5}
`)
	lead := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "w"}))
	for _, n := range []string{"a", "b"} {
		must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: n, Task: "t"}))
	}
	setWorking(e, "a", ago(2*time.Minute), "")
	e.Tick()
	if len(engineMailTo(e, mustID(e, "b"), "watch")) != 1 || len(engineMailTo(e, mustID(e, "a"), "watch")) != 0 {
		t.Fatal("role target")
	}
}

func TestTaskforceTick(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	caller := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	must(e.Agent(caller.Token, AgentArgs{Action: "spawn", Template: "advisor", Name: "adv", Task: "?"}))

	// Idle for idle_for: the caller is told once per idle stretch.
	e.mu.Lock()
	_, _ = e.db.Exec(`UPDATE participants SET status=?, state_since=? WHERE name=?`, StatusLive, ago(21*time.Minute), "adv-advisor")
	_, _ = e.db.Exec(`UPDATE participants SET state_since=? WHERE name=?`, ago(21*time.Minute), "adv-advisor")
	e.mu.Unlock()
	e.Tick()
	e.Tick()
	idle := engineMailTo(e, caller.ID, "taskforce")
	if len(idle) != 1 || !strings.Contains(idle[0], "Taskforce adv (template advisor) has been idle for 21m") ||
		!strings.Contains(idle[0], "agent action=close team=adv") {
		t.Fatalf("idle notice: %q", idle)
	}
	// Its chair stopped: closed, the caller told.
	must(e.Kill("adv-advisor"))
	eventually(t, e, "chair stopped", func() bool { return e.runs[mustID(e, "adv-advisor")] == nil })
	e.Tick()
	if teamOf(t, e, "adv").Status != TeamClosed {
		t.Fatal("taskforce with a stopped chair is open")
	}
	if got := engineMailTo(e, caller.ID, "taskforce"); len(got) != 2 || got[1] != "Taskforce adv closed: its chair adv-advisor stopped." {
		t.Fatalf("closed notice: %q", got)
	}

	// Its caller gone: closed (here the caller's row is gone without the sweep closing it first).
	must(e.Agent(caller.Token, AgentArgs{Action: "spawn", Template: "advisor", Name: "adv2", Task: "?"}))
	e.mu.Lock()
	_, _ = e.db.Exec(`UPDATE participants SET status=? WHERE id=?`, StatusGone, caller.ID)
	e.mu.Unlock()
	e.Tick()
	if teamOf(t, e, "adv2").Status != TeamClosed {
		t.Fatal("orphan taskforce open")
	}
	// Its caller's team closed: closed.
	g := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	must(e.Agent(g.Token, AgentArgs{Action: "found", Template: "slp"}))
	must(e.Agent(g.Token, AgentArgs{Action: "spawn", Role: "lead", Name: "l", Task: "t"}))
	must(e.Agent(tokenOf(t, e, "l"), AgentArgs{Action: "spawn", Template: "advisor", Name: "adv3", Task: "?"}))
	e.mu.Lock()
	_, _ = e.db.Exec(`UPDATE teams SET status=? WHERE id=?`, TeamClosed, mustTeam2(e, "l"))
	e.mu.Unlock()
	e.Tick()
	if teamOf(t, e, "adv3").Status != TeamClosed {
		t.Fatal("taskforce of a closed team open")
	}
}

func mustTeam2(e *Engine, name string) string {
	p, _ := e.pWhere(`name=?`, name)
	return p.TeamID
}

// The gate's notices at the end of a turn on mail: reply, settled, failed; none for a member, a
// turn without mail, or a turn ending with mail left.
func TestGateNotices(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	g := hooked(t, e, "G")
	must(e.Agent(g.Token, AgentArgs{Action: "found", Template: "lead-peer", Name: "api"}))
	must(e.Agent(g.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "w", Task: "t"}))
	w := tokenOf(t, e, "w")
	turn := func(id string, ok bool, send bool) {
		ev(t, e, HarnessEventArgs{Key: "G", Event: HarnessTurnStart, PromptID: id})
		if send {
			must(e.Send(g.Token, SendArgs{To: "w", Body: "next"}))
			eventually(t, e, "w read it", func() bool { return unackedToLocked(e, mustID(e, "w")) == 0 })
		}
		ev(t, e, HarnessEventArgs{Key: "G", Event: HarnessTurnEnd, PromptID: id, OK: ok})
	}
	turn("chat", true, false) // no mail in it: nothing
	must(e.Send(w, SendArgs{To: g.Name, Body: "done"}))
	turn("t1", true, false)
	must(e.Send(w, SendArgs{To: g.Name, Body: "done again"}))
	turn("t2", true, true)
	must(e.Send(w, SendArgs{To: g.Name, Body: "broken"}))
	turn("t3", false, false)
	if r := noticesOf(e, "reply"); len(r) != 1 || r[0] != g.Name+" (gate of api) finished a turn on mail and sent nothing: its answer is in its session." {
		t.Fatalf("reply: %q", r)
	}
	if s := noticesOf(e, "settled"); len(s) != 1 || !strings.Contains(s[0], "the team is settled") {
		t.Fatalf("settled: %q", s)
	}
	if f := noticesOf(e, "failed"); len(f) != 1 || !strings.Contains(f[0], "failed a turn on mail") {
		t.Fatalf("failed: %q", f)
	}
	v := must(e.View(0))
	if len(v.Notices) != 3 || v.Notices[0].Kind != "failed" || v.Notices[0].Where != "api" || v.Notices[0].Age == "" {
		t.Fatalf("view notices: %+v", v.Notices)
	}
	// A member that is not the gate writes none.
	m := hooked(t, e, "M")
	e.mu.Lock()
	tm, _ := e.tWhere(`name=?`, "api")
	_, _ = e.db.Exec(`UPDATE participants SET team_id=?, role='peer', spawned_by=? WHERE id=?`, tm.ID, g.ID, m.ID)
	e.mu.Unlock()
	must(e.Send(g.Token, SendArgs{To: m.Name, Body: "for m"}))
	ev(t, e, HarnessEventArgs{Key: "M", Event: HarnessTurnStart, PromptID: "m1"})
	ev(t, e, HarnessEventArgs{Key: "M", Event: HarnessTurnEnd, PromptID: "m1", OK: true})
	if n := len(must(e.View(0)).Notices); n != 3 {
		t.Fatalf("a member's turn wrote a notice: %d", n)
	}
	if shortAge(30*time.Second) != "30s" || shortAge(5*time.Minute) != "5m" || shortAge(3*time.Hour) != "3h" || shortAge(72*time.Hour) != "3d" {
		t.Fatal("shortAge")
	}
}

func TestWhyLogDoctor(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	writeTemplate(t, e, "rt", routingTemplate)
	lead := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "rt", Name: "rt"}))
	for _, n := range []string{"d1", "d2"} {
		must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "dev", Name: n, Task: "t"}))
	}
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "qa", Name: "q1", Task: "t"}))
	solo := must(e.RegisterSession(t.TempDir(), "", "", "claude"))

	lines, verdict := must2(e.Why("d1", lead.Name))
	if verdict != "allow" || lines[len(lines)-1].Check != "cc" || !strings.Contains(lines[len(lines)-1].Detail, "q1") {
		t.Fatalf("why d1 -> lead: %s %+v", verdict, lines)
	}
	_, verdict = must2(e.Why("d1", "d2"))
	if verdict != "deny" {
		t.Fatal(verdict)
	}
	_, verdict = must2(e.Why("d1", "notify"))
	if verdict != "deny" {
		t.Fatal(verdict)
	}
	lines, verdict = must2(e.Why(lead.Name, "board"))
	if verdict != "allow" || lines[len(lines)-1].Rule != "can_pin" {
		t.Fatalf("why board: %+v", lines)
	}
	_, verdict = must2(e.Why("d1", "board"))
	if verdict != "deny" {
		t.Fatal(verdict)
	}
	lines, verdict = must2(e.Why(solo.Name, "rt"))
	if verdict != "allow" || lines[1].Check != "team_gate" {
		t.Fatalf("why solo -> team: %+v", lines)
	}
	must(e.Send(tokenOf(t, e, "d1"), SendArgs{To: lead.Name, Body: "1"}))
	must(e.Send(tokenOf(t, e, "d1"), SendArgs{To: lead.Name, Body: "2"}))
	_, verdict = must2(e.Why("d1", lead.Name))
	if verdict != "hold" {
		t.Fatal(verdict)
	}
	if _, _, err := e.Why("ghost", lead.Name); err == nil {
		t.Fatal("why from a ghost")
	}

	evs := must(e.Log(0, "", 0))
	if len(evs) == 0 || evs[0].Kind != "found" {
		t.Fatalf("log: %+v", evs)
	}
	if rt := must(e.Log(evs[0].Seq, "rt", 2)); len(rt) != 2 || rt[0].Seq <= evs[0].Seq {
		t.Fatalf("log after, team, limit: %+v", rt)
	}

	if fs := must(e.Doctor()); len(fs) != 0 {
		t.Fatalf("doctor with nothing stuck: %+v", fs)
	}
	must(e.Send(tokenOf(t, e, "d1"), SendArgs{To: lead.Name, Body: "3 (held)"}))
	gone := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	must(e.Send(solo.Token, SendArgs{To: gone.Name, Body: "unread"}))
	e.mu.Lock()
	_, _ = e.db.Exec(`UPDATE participants SET status=? WHERE id=?`, StatusGone, gone.ID)
	_, _ = e.db.Exec(`UPDATE participants SET status=? WHERE name='d2'`, StatusParked)
	_, _ = e.db.Exec(`UPDATE participants SET status=?, state_since=?, last_turn_end='' WHERE name='q1'`, StatusWorking, ago(time.Hour))
	_, _ = e.db.Exec(`UPDATE participants SET state_since=? WHERE name='q1'`, ago(time.Hour))
	delete(e.runs, mustID(e, "d1")) // a live worker squad has no process for
	e.mu.Unlock()
	kinds := map[string]bool{}
	for _, f := range must(e.Doctor()) {
		kinds[f.Kind] = true
	}
	for _, k := range []string{"held", "gone_unacked", "parked", "stuck_turn", "no_process"} {
		if !kinds[k] {
			t.Errorf("doctor missed %s: %v", k, kinds)
		}
	}
}

func must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(err)
	}
	return a, b
}

func unackedToLocked(e *Engine, id string) int {
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE to_id=? AND acked_at=''`, id).Scan(&n)
	return n
}
