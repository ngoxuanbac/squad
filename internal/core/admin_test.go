package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bacnx/squad/internal/driver"
)

func TestAdminOps(t *testing.T) {
	claude := &memRuntime{name: "claude"}
	e := memEngine(t, claude)
	lead := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "lead-peer", Name: "api"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "p1", Task: "t"}))

	if p := must(e.Resolve("p1")); p.Kind != KindWorker {
		t.Fatalf("resolve: %+v", p)
	}
	if p := must(e.Resolve(mustID(e, "p1"))); p.Name != "p1" {
		t.Fatal("resolve by id")
	}
	_, err := e.Resolve("nobody")
	refused(t, err, "not_found")

	if out := must(e.Interrupt("p1")); !strings.Contains(out, "interrupted") {
		t.Fatal(out)
	}
	_, err = e.Interrupt(lead.Name)
	refused(t, err, "invalid")
	_, err = e.Kill(lead.Name)
	refused(t, err, "invalid")
	_, err = e.Resume(lead.Name)
	refused(t, err, "invalid")

	must(e.Kill("p1"))
	eventually(t, e, "p1 stopped", func() bool { return e.runs[mustID(e, "p1")] == nil })
	if out := must(e.Kill("p1")); !strings.Contains(out, "not running") {
		t.Fatal(out)
	}
	_, err = e.Interrupt("p1")
	refused(t, err, "invalid")
	must(e.Resume("p1"))
	if claude.count() != 2 || !claude.last().Resume {
		t.Fatal("resume did not start the worker again")
	}
	if out := must(e.Resume("p1")); !strings.Contains(out, "already running") || claude.count() != 2 {
		t.Fatalf("resume of a running worker: %s", out)
	}

	_, err = e.TeamDown("nope")
	refused(t, err, "not_found")
	must(e.TeamDown("api"))
	_, err = e.Resume("p1")
	refused(t, err, "closed")
}

func TestWhoAndTeamText(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	lead := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	solo := must(e.RegisterSession(t.TempDir(), "", "", "claude"))

	who := must(e.Who(lead.Token))
	if !strings.Contains(who, "solo session") || !strings.Contains(who, "solo "+solo.Name) {
		t.Fatalf("who as solo: %s", who)
	}
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "lead-peer", Name: "api"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "p1", Task: "fix it"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Template: "advisor", Name: "adv", Task: "?"}))

	who = must(e.Who(lead.Token))
	for _, want := range []string{"role lead, in team api", "p1 (peer, worker", "you may write", "task: fix it",
		"Your taskforce: adv", "team adv (advisor, taskforce of " + lead.Name, "solo " + solo.Name} {
		if !strings.Contains(who, want) {
			t.Errorf("who lacks %q:\n%s", want, who)
		}
	}
	team := must(e.Agent(must(e.Whoami(tokenOf(t, e, "p1"))).Token, AgentArgs{Action: "team"}))
	if !strings.Contains(team, "reports to "+lead.Name) || !strings.Contains(team, "[gate") {
		t.Errorf("team as p1:\n%s", team)
	}
	chair := tokenOf(t, e, "adv-advisor")
	if text := must(e.Who(chair)); !strings.Contains(text, `to="caller"`) {
		t.Errorf("who as chair:\n%s", text)
	}
	if text := must(e.Agent(solo.Token, AgentArgs{Action: "team"})); !strings.Contains(text, "no team") {
		t.Errorf("team as solo: %s", text)
	}
	_, err := e.Who("bad-token")
	refused(t, err, "auth")
}

func TestStopFor(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	sup := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	must(e.Agent(sup.Token, AgentArgs{Action: "found", Template: "slp"}))
	must(e.Agent(sup.Token, AgentArgs{Action: "spawn", Role: "lead", Name: "l1", Task: "t"}))
	l1 := tokenOf(t, e, "l1")
	must(e.Agent(l1, AgentArgs{Action: "spawn", Role: "peer", Name: "p1", Task: "t"}))
	must(e.Agent(sup.Token, AgentArgs{Action: "spawn", Role: "lead", Name: "l2", Task: "t"}))

	_, err := e.Agent(l1, AgentArgs{Action: "stop", Name: "l2"})
	refused(t, err, "permission")
	_, err = e.Agent(l1, AgentArgs{Action: "stop", Name: "nobody"})
	refused(t, err, "not_found")
	must(e.Agent(l1, AgentArgs{Action: "stop", Name: "p1"})) // its own worker
	must(e.Agent(sup.Token, AgentArgs{Action: "stop", Name: "l2"}))
	eventually(t, e, "stopped", func() bool { return len(e.runs) == 1 })
}

func TestAgentRefusals(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	lead := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	solo := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	cases := []struct {
		token string
		args  AgentArgs
		code  string
	}{
		{lead.Token, AgentArgs{Action: "dance"}, "invalid"},
		{lead.Token, AgentArgs{Action: "spawn", Role: "peer", Task: "t"}, "invalid"}, // no team yet
		{lead.Token, AgentArgs{Action: "found", Template: "nope"}, "not_found"},
		{lead.Token, AgentArgs{Action: "close"}, "invalid"},
		{lead.Token, AgentArgs{Action: "close", Team: "nope"}, "permission"},
		{lead.Token, AgentArgs{Action: "spawn", Template: "lead-peer", Task: "t"}, "invalid"}, // not a taskforce
		{lead.Token, AgentArgs{Action: "spawn", Template: "advisor"}, "invalid"},              // no task
		{lead.Token, AgentArgs{Action: "spawn", Template: "advisor", Role: "x", Task: "t"}, "invalid"},
		{lead.Token, AgentArgs{Action: "spawn", Template: "nope", Task: "t"}, "not_found"},
	}
	for _, c := range cases {
		_, err := e.Agent(c.token, c.args)
		refused(t, err, c.code)
	}
	if out := must(e.Agent(lead.Token, AgentArgs{Action: "templates"})); !strings.Contains(out, "dual-lens (built-in) [taskforce") {
		t.Fatalf("templates: %s", out)
	}

	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "lead-peer"}))
	more := []struct {
		args AgentArgs
		code string
	}{
		{AgentArgs{Action: "spawn", Role: "lead", Task: "t"}, "permission"},
		{AgentArgs{Action: "spawn", Role: "peer"}, "invalid"}, // no task
		{AgentArgs{Action: "spawn", Role: "peer", Task: "t", Cwd: "nowhere"}, "invalid"},
	}
	for _, c := range more {
		_, err := e.Agent(lead.Token, c.args)
		refused(t, err, c.code)
	}
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "p1", Task: "t"}))
	p1 := tokenOf(t, e, "p1")
	_, err := e.Agent(p1, AgentArgs{Action: "templates"})
	if err != nil {
		t.Fatalf("templates need no agent tool: %v", err)
	}
	_, err = e.Agent(p1, AgentArgs{Action: "spawn", Role: "peer", Task: "t"})
	refused(t, err, "permission") // peer has no agent tool
	_, err = e.Agent(solo.Token, AgentArgs{Action: "close", Team: "lead"})
	refused(t, err, "permission")
	// A worker cannot found a team.
	e.mu.Lock()
	_, _ = e.db.Exec(`UPDATE participants SET team_id='' WHERE name='p1'`)
	e.mu.Unlock()
	_, err = e.Agent(p1, AgentArgs{Action: "found", Template: "p2p"})
	refused(t, err, "invalid")
}

func TestSpawnLimits(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	writeTemplate(t, e, "tiny", `template: tiny
auto_join_role: lead
roles:
  lead: {tools: [send, inbox, who, agent], can_spawn: [peer], can_set_cwd: true}
  peer: {tools: [send, agent], can_spawn: [peer]}
routing:
  - {from: lead, to: peer}
  - {from: peer, to: lead}
  - {from: peer, to: peer}
limits: {depth: 2, concurrency: 2, messages_per_participant_per_minute: 3, max_respawn_per_hour: none}
`)
	root := t.TempDir()
	lead := must(e.RegisterSession(root, "", "", "claude"))
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "tiny"}))
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	out := must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "p1", Task: "t", Cwd: "sub"}))
	if !strings.Contains(out, sub) {
		t.Fatalf("cwd not used: %s", out)
	}
	p1 := tokenOf(t, e, "p1")
	_, err := e.Agent(p1, AgentArgs{Action: "spawn", Role: "peer", Task: "t", Cwd: "sub"})
	refused(t, err, "permission") // peer has no can_set_cwd
	must(e.Agent(p1, AgentArgs{Action: "spawn", Role: "peer", Name: "p2", Task: "t"}))
	_, err = e.Agent(tokenOf(t, e, "p2"), AgentArgs{Action: "spawn", Role: "peer", Task: "t"})
	refused(t, err, "limit") // depth 2
	_, err = e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Task: "t"})
	refused(t, err, "limit") // concurrency 2
	_, err = e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Task: "t", Cwd: t.TempDir()})
	refused(t, err, "bounds")

	must(e.Send(lead.Token, SendArgs{To: "p1", Body: "1"}))
	must(e.Send(lead.Token, SendArgs{To: "p1", Body: "2"}))
	// messages_per_participant_per_minute 3, and the spawn's task was the first: held, not refused.
	if r := must(e.Send(lead.Token, SendArgs{To: "p1", Body: "3"})); !r.Held || r.Rule != RuleRate {
		t.Fatalf("over the rate: %+v", r)
	}
}

// A worktree of the team's repository is inside the bounds, though outside its root.
func TestBoundsAllowGitWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(root, "init", "-q")
	run(root, "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(t.TempDir(), "lane")
	run(root, "worktree", "add", "-q", wt)
	e := memEngine(t)
	if dir, err := e.bounded(root, root, wt); err != nil || dir != filepath.Clean(wt) {
		t.Fatalf("worktree refused: %q %v", dir, err)
	}
	if _, err := e.bounded(root, root, t.TempDir()); err == nil {
		t.Fatal("unrelated directory allowed")
	}
	if !within(root, filepath.Join(root, "a", "b")) || within(root, filepath.Dir(root)) {
		t.Fatal("within")
	}
}

func TestSweepMarksSilentSessionsGone(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	s := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	must(e.Agent(s.Token, AgentArgs{Action: "spawn", Template: "advisor", Name: "adv", Task: "?"}))
	e.mu.Lock()
	old := time.Now().Add(-2 * SessionTimeout).UTC().Format(time.RFC3339)
	_, _ = e.db.Exec(`UPDATE participants SET last_active=? WHERE id=?`, old, s.ID)
	e.mu.Unlock()
	e.Sweep()
	e.mu.Lock()
	p, _ := e.byID(s.ID)
	tf, _ := e.tWhere(`name=?`, "adv")
	e.mu.Unlock()
	if p.Status != StatusGone || tf.Status != TeamClosed {
		t.Fatalf("after sweep: session %s, taskforce %s", p.Status, tf.Status)
	}
	// A heartbeat brings it back.
	must(e.Heartbeat(s.Token))
	if me := must(e.Whoami(s.Token)); me.Name != s.Name {
		t.Fatal(me)
	}
	e.mu.Lock()
	p, _ = e.byID(s.ID)
	e.mu.Unlock()
	if p.Status != StatusLive {
		t.Fatalf("after heartbeat: %s", p.Status)
	}
}

func TestSetModel(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude", levels: []string{"low", "high"}})
	lead := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "lead-peer", Name: "api"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "w1", Task: "t"}))

	_, err := e.SetModel("w1", "", "")
	refused(t, err, "invalid")
	_, err = e.SetModel(lead.Name, "opus", "")
	refused(t, err, "invalid") // a session
	_, err = e.SetModel("ghost", "opus", "")
	refused(t, err, "not_found")
	_, err = e.SetModel("w1", "bad", "")
	refused(t, err, "invalid")
	_, err = e.SetModel("w1", "", "turbo") // a level the harness does not run
	refused(t, err, "invalid")
	if p := pOf(t, e, "w1"); p.Model == "bad" {
		t.Fatal("a refused model was stored")
	}

	out := must(e.SetModel("w1", "opus", "high"))
	if !strings.Contains(out, "model opus, thinking high, from its next turn") {
		t.Fatal(out)
	}
	must(e.Kill("w1"))
	eventually(t, e, "w1 stopped", func() bool { return e.runs[mustID(e, "w1")] == nil })
	out = must(e.SetModel("w1", "", "low"))
	if !strings.Contains(out, "model opus, thinking low, from its next start") {
		t.Fatal(out)
	}
	if orDefault("") != "(harness default)" {
		t.Fatal(orDefault(""))
	}
}

// config.yaml's spawn.allowed_roots and prompts reach the engine as Options.
func TestAllowedRootsAndSharedPrompts(t *testing.T) {
	extra := t.TempDir()
	rt := &memRuntime{name: "claude"}
	e, err := Open(Options{Dir: t.TempDir(), Version: "test", Runtimes: []driver.Runtime{rt}, AllowedRoots: []string{extra},
		Prompts: func(template, role string) string { return "\n## Shared prompt for " + template + "/" + role + "\n" }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	root := t.TempDir()
	_ = os.Mkdir(filepath.Join(extra, "sub"), 0o700)
	if dir, err := e.bounded(root, root, filepath.Join(extra, "sub")); err != nil || dir != filepath.Join(extra, "sub") {
		t.Fatalf("allowed root refused: %q %v", dir, err)
	}
	_, err = e.bounded(root, root, t.TempDir())
	refusedRule(t, err, "cwd.bounds")

	// A solo session's card at its start, then a worker's role card.
	r := ev(t, e, HarnessEventArgs{Key: "S", Harness: "claude", Event: HarnessSessionStart, Cwd: root, Source: "startup"})
	if !strings.Contains(r.Text, "Shared prompt for /solo") {
		t.Fatalf("session start: %q", r.Text)
	}
	lead := hooked(t, e, "L")
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "lead-peer", Name: "api"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "w1", Task: "t"}))
	if card := rt.last().RoleCard; !strings.Contains(card, "Shared prompt for lead-peer/peer") {
		t.Fatalf("role card: %q", card)
	}
}
