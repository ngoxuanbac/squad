package e2e

import (
	"strings"
	"testing"
)

// A session founds a team, briefs a worker, gets its handback and closes the team.
func TestRoundTrip(t *testing.T) {
	e := newEnv(t)
	lead := e.session(t.TempDir())
	if got := lead.ok(do("agent", map[string]any{"action": "templates"})); !strings.Contains(got, "lead-peer") {
		t.Fatalf("templates: %s", got)
	}
	lead.ok(do("agent", map[string]any{"action": "found", "template": "lead-peer", "name": "t1"}))
	lead.ok(spawn("peer", "p1", do("send", map[string]any{"to": lead.name, "body": "done", "kind": "handback"})))
	got := lead.mail("done")
	if !strings.Contains(got, `sender="p1"`) || !strings.Contains(got, `kind="handback"`) {
		t.Fatalf("handback: %s", got)
	}
	e.waitStatus("p1", "idle")
	if got := lead.ok(do("agent", map[string]any{"action": "team"})); !strings.Contains(got, "p1") {
		t.Fatalf("team: %s", got)
	}

	lead.ok(do("agent", map[string]any{"action": "close"}))
	e.waitStatus("p1", "stopped")
	v := e.ps()
	if len(v.Teams) != 1 || v.Teams[0].Status != "closed" || len(v.Solos) != 1 || v.Solos[0].Name != lead.name {
		t.Fatalf("after close: %+v", v)
	}
}

// team is the named team of ps.
func (e *env) team(name string) (status, gate string) {
	e.t.Helper()
	for _, t := range e.ps().Teams {
		if t.Name == name {
			return t.Status, t.Gate
		}
	}
	return "", ""
}

func TestTeamDownAndReopen(t *testing.T) {
	e := newEnv(t)
	lead := e.session(t.TempDir())
	lead.ok(do("agent", map[string]any{"action": "found", "template": "lead-peer", "name": "t1"}))
	lead.ok(spawn("peer", "p1"))
	e.waitStatus("p1", "idle")

	e.squad("team", "down", "t1")
	e.waitStatus("p1", "stopped")
	if status, _ := e.team("t1"); status != "closed" || e.member(lead.name).Role != "" {
		t.Fatalf("after team down: %s, lead %+v", status, e.member(lead.name))
	}
	wantRule(t, lead.refused(send("p1", "x")), "closed")

	// Reopened by a session at its root: the gate again, its worker stopped until mail comes.
	lead.ok(do("agent", map[string]any{"action": "reopen", "team": "t1"}))
	if status, gate := e.team("t1"); status != "open" || gate != lead.name || e.member("p1").Status != "stopped" {
		t.Fatalf("after reopen: %s gate %s, p1 %+v", status, gate, e.member("p1"))
	}
	lead.ok(send("p1", send(lead.name, "back")))
	lead.mail("back")
}

// A second session at the root is admitted; when the gate founds another team it leaves, and the
// session becomes the gate and gets the leaver's workers.
func TestAdmitAndGateMoves(t *testing.T) {
	e := newEnv(t)
	root := t.TempDir()
	lead := e.session(root)
	lead.ok(do("agent", map[string]any{"action": "found", "template": "lead-peer", "name": "t1"}))
	lead.ok(spawn("peer", "p1"))
	other := e.session(root)
	lead.ok(do("agent", map[string]any{"action": "admit", "name": other.name, "role": "peer"}))
	if m := e.member(other.name); m.Role != "peer" {
		t.Fatalf("admitted: %+v", m)
	}
	other.ok(send(lead.name, "hi lead"))
	lead.mail("hi lead")

	lead.ok(do("agent", map[string]any{"action": "found", "template": "p2p", "name": "t2"}))
	if _, gate := e.team("t1"); gate != other.name {
		t.Fatalf("t1's gate is %q, want %s", gate, other.name)
	}
	if !strings.Contains(e.squad("log", "--no-start"), "gate_moved") {
		t.Fatal("no gate_moved event")
	}
	// p1 reported to the leaver; its mail now goes to the new gate.
	lead.refused(send("p1", "x"))
	other.ok(send("p1", send(other.name, "to new gate")))
	other.mail("to new gate")
}

func TestTaskforce(t *testing.T) {
	e := newEnv(t)
	caller := e.session(t.TempDir())
	brief := lines(
		send("caller", "advice"),
		do("agent", map[string]any{"action": "spawn", "template": "advisor", "task": "x"}),
	)
	caller.ok(do("agent", map[string]any{"action": "spawn", "template": "advisor", "name": "tf1", "task": brief}))
	caller.mail("advice")
	_, chair := e.team("tf1")
	waitFor(t, "the chair's calls", func() bool { c := e.toolCalls(chair); return len(c) == 2 && c[1].Done })
	if c := e.toolCalls(chair)[1]; !c.Err {
		t.Fatalf("a taskforce called a taskforce: %s", c.Text)
	}

	caller.ok(do("agent", map[string]any{"action": "close", "team": "tf1"}))
	if status, _ := e.team("tf1"); status != "closed" {
		t.Fatalf("tf1 %s after close", status)
	}

	// A taskforce does not outlive the session that called it.
	caller.ok(do("agent", map[string]any{"action": "spawn", "template": "advisor", "name": "tf2", "task": "brief"}))
	caller.quit()
	waitFor(t, "tf2 closed", func() bool { status, _ := e.team("tf2"); return status == "closed" })
}
