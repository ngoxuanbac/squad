package e2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bacnx/squad/internal/driver"
)

// Sessions with squad's hooks plugin: mail comes with their turns (at the start, after a tool
// call, held at the end), is acked when the turn ends normally, and an idle one is woken for it.

// sends are the bodies of the send calls to whom among calls.
func sends(calls []call, whom string) []string {
	var out []string
	for _, c := range calls {
		var in struct{ To, Body string }
		if c.Tool == "send" && json.Unmarshal([]byte(c.Input), &in) == nil && in.To == whom {
			out = append(out, in.Body)
		}
	}
	return out
}

// hookedLead founds a lead-peer team from a session with squad's hooks plugin and spawns p1.
func hookedLead(t *testing.T, e *env) *agent {
	t.Helper()
	lead := e.open(t.TempDir(), driver.NewUUID(), true)
	lead.ok(do("agent", map[string]any{"action": "found", "template": "lead-peer"}))
	lead.ok(spawn("peer", "p1"))
	return lead
}

func TestHookedSessionIsWokenForMail(t *testing.T) {
	e := newEnv(t)
	lead := hookedLead(t, e)
	// p1's handback comes while the lead is idle: squad wakes it, the mail comes with the turn,
	// and it is acked when the turn ends.
	lead.ok(send("p1", "@sleep 500ms", send(lead.name, send("p1", "thanks"))))
	if got := sends(lead.woken(), "p1"); len(got) != 1 || got[0] != "thanks" {
		t.Fatalf("woken turn sent %q", got)
	}
	waitFor(t, "the lead's mail acked", func() bool { return e.member(lead.name).Unread == 0 })
}

// stopHeld is the lead's work for its turn: send p1 a message that p1 answers a moment later
// with a message telling the lead to do it again, n times deep, the lead waiting each time long
// enough for the answer to come before its turn ends.
func stopHeld(lead *agent, n int) string {
	work := ""
	for i := n; i >= 1; i-- {
		answer := lines("@sleep 300ms", send(lead.name, work))
		work = lines(send("p1", answer), "@sleep 1s")
	}
	return work
}

// stops are the Stop hooks of the session's last turn: held back or not.
func stops(a *agent) (held, let int) {
	for _, h := range a.hooks {
		if strings.HasPrefix(h, "Stop ") {
			if strings.Contains(h, `"decision":"block"`) {
				held++
			} else {
				let++
			}
		}
	}
	return held, let
}

func TestStopHoldsTheTurnForMail(t *testing.T) {
	e := newEnv(t)
	lead := hookedLead(t, e)
	// Each answer comes while the turn works, so its end is held back to read it: three times at
	// most, then the turn ends and the fourth answer wakes the lead afterwards.
	calls := lead.turn(stopHeld(lead, 6))
	if held, let := stops(lead); held != 3 || let != 1 {
		t.Fatalf("turn end held %d times, let %d; hooks:\n%s", held, let, strings.Join(lead.hooks, "\n"))
	}
	if n := len(sends(calls, "p1")); n != 4 {
		t.Fatalf("the turn sent p1 %d messages, want 1 + 3 held ends", n)
	}
	if n := len(sends(lead.woken(), "p1")); n == 0 {
		t.Fatal("the answer left after the third hold did not wake the lead")
	}
}

func TestPermissionHoldsMail(t *testing.T) {
	e := newEnv(t)
	lead := hookedLead(t, e)
	id := lead.start(lines(
		send("p1", "@sleep 300ms", send(lead.name, send("p1", "read after the prompt"))),
		"@permission", "@await answered", do("who", nil)))
	e.waitStatus(lead.name, "permission")
	time.Sleep(500 * time.Millisecond) // p1's answer comes now: it waits, the session is not woken
	if s := e.member(lead.name).Status; s != "permission" {
		t.Fatalf("status %s while its person decides", s)
	}
	e.release("answered") // the tool runs: the mail comes after it
	calls := lead.until(id, "the turn after the prompt")
	if got := sends(calls, "p1"); len(got) != 2 || got[1] != "read after the prompt" {
		t.Fatalf("sends to p1: %q", got)
	}
}

func TestEscGivesMailAgain(t *testing.T) {
	e := newEnv(t)
	lead := hookedLead(t, e)
	// The answer comes after the who call; the person presses Esc while the model reads it, so it
	// comes again with the next turn: the one the person starts (squad would wake it otherwise, on
	// its next look, WakeEvery later).
	answer := lines("@sleep 300ms", send(lead.name, once("esc", "@esc"), send("p1", "after esc")))
	calls := lead.turn(lines(send("p1", answer), "@sleep 700ms", do("who", nil)))
	if got := sends(calls, "p1"); len(got) != 1 {
		t.Fatalf("the cancelled turn sent %q", got)
	}
	if got := sends(lead.turn("go on"), "p1"); len(got) != 1 || got[0] != "after esc" {
		t.Fatalf("the next turn sent %q", got)
	}
}

// Mail for a member session its person closed resumes it as a headless worker; when the person
// opens the session again, it is a session again.
func TestGoneMemberIsResumedHeadless(t *testing.T) {
	e := newEnv(t)
	root := t.TempDir()
	lead := e.founder(root, "lead-peer")
	peer := e.session(root)
	lead.ok(do("agent", map[string]any{"action": "admit", "name": peer.name, "role": "peer"}))
	peer.quit()
	e.waitStatus(peer.name, "gone")

	lead.ok(send(peer.name, send(lead.name, "from headless")))
	lead.mail("from headless")
	if m := e.member(peer.name); m.Kind != "worker" {
		t.Fatalf("woken member: %+v", m)
	}

	again := e.open(root, peer.id, false)
	if again.name != peer.name {
		t.Fatalf("reopened as %s, was %s", again.name, peer.name)
	}
	if m := e.member(peer.name); m.Kind != "session" || m.Role != "peer" {
		t.Fatalf("reopened member: %+v", m)
	}
}
