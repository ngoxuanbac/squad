package e2e

import (
	"strings"
	"testing"
)

// Mail is acked only when the turn that got it completes: a turn cut short by a crash, an abort, a
// kill or the daemon going down gives the mail again, and mail acked once never comes again.

// leadWithPeer founds a lead-peer team and spawns p1 with task; p1's handback is "done".
func leadWithPeer(t *testing.T, e *env, task ...string) *agent {
	t.Helper()
	lead := e.founder(t.TempDir(), "lead-peer")
	lead.ok(spawn("peer", "p1", append(task, send(lead.name, "done"))...))
	return lead
}

// nudge is new mail from the lead to p1, which resumes a stopped p1.
func nudge(lead *agent, ds ...string) {
	lead.t.Helper()
	lead.ok(send("p1", append([]string{"nudge"}, ds...)...))
}

func TestCrashGivesMailAgain(t *testing.T) {
	e := newEnv(t)
	lead := leadWithPeer(t, e, once("crash", "@exit 3"))
	e.waitStatus("p1", "stopped")
	nudge(lead)
	lead.mail("done")
	if tail := e.squad("tail", "p1", "--json", "--no-start"); !strings.Contains(tail, `redelivered=\"true\"`) {
		t.Fatalf("the task did not come again as redelivered:\n%s", tail)
	}
}

func TestAbortGivesMailAgain(t *testing.T) {
	e := newEnv(t)
	lead := leadWithPeer(t, e, once("nap", "@sleep 1m"))
	e.waitStatus("p1", "working")
	e.squad("abort", "p1")
	lead.mail("done") // no new mail needed: p1 is alive and idle again
}

// A worker killed, or gone with the daemon, mid-turn gets its task again with the next mail.
func TestStoppedMidTurnGivesMailAgain(t *testing.T) {
	for name, cut := range map[string]func(e *env){
		"kill":         func(e *env) { e.squad("kill", "p1") },
		"daemon crash": func(e *env) { e.crash(); e.start() }, // the session's squad mcp finds the new daemon
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			lead := leadWithPeer(t, e, once("nap", "@sleep 1m"))
			e.waitStatus("p1", "working")
			cut(e)
			e.waitStatus("p1", "stopped")
			nudge(lead)
			lead.mail("done")
		})
	}
}

// The lead resumes a killed worker itself and reads its log through the agent tool.
func TestAgentResumeAndTail(t *testing.T) {
	e := newEnv(t)
	lead := leadWithPeer(t, e, once("nap", "@sleep 1m"))
	e.waitStatus("p1", "working")
	lead.ok(do("agent", map[string]any{"action": "stop", "name": "p1"}))
	e.waitStatus("p1", "stopped")
	lead.ok(do("agent", map[string]any{"action": "resume", "name": "p1"}))
	lead.mail("done")
	if tail := lead.ok(do("agent", map[string]any{"action": "tail", "name": "p1"})); !strings.Contains(tail, "mcp__squad__send") {
		t.Fatalf("tail:\n%s", tail)
	}
}

func TestAckedMailComesOnce(t *testing.T) {
	e := newEnv(t)
	lead := leadWithPeer(t, e)
	lead.mail("done")
	e.waitStatus("p1", "idle")
	e.squad("kill", "p1")
	e.waitStatus("p1", "stopped")
	nudge(lead, send(lead.name, "second"))
	if got := lead.mail("second"); strings.Contains(got, "done") {
		t.Fatalf("acked task ran again:\n%s", got)
	}
	if n := strings.Count(e.squad("tail", "p1", "--json", "--no-start"), `redelivered=\"true\"`); n != 0 {
		t.Fatalf("%d redelivered", n)
	}
}
