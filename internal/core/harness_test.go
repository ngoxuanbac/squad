package core

import (
	"context"
	"strings"
	"testing"
	"time"
)

// hooked registers a session through its SessionStart hook, as `squad hook claude` does.
func hooked(t *testing.T, e *Engine, key string) Me {
	t.Helper()
	if _, err := e.HarnessEvent(HarnessEventArgs{Key: key, Harness: "claude", Event: HarnessSessionStart, Cwd: t.TempDir(), Source: "startup"}); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	p, err := e.pWhere(`session_ref=?`, key)
	e.mu.Unlock()
	if err != nil || p == nil || !p.Hooked {
		t.Fatalf("session %s not hooked: %+v %v", key, p, err)
	}
	return must(e.Whoami(p.Token))
}

func ev(t *testing.T, e *Engine, a HarnessEventArgs) HarnessEventResult {
	t.Helper()
	r, err := e.HarnessEvent(a)
	if err != nil {
		t.Fatalf("%s: %v", a.Event, err)
	}
	return r
}

func statusOf(t *testing.T, e *Engine, id string) string {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	p, _ := e.byID(id)
	return p.Status
}

func unackedTo(e *Engine, id string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE to_id=? AND acked_at=''`, id).Scan(&n)
	return n
}

// Mail is given with a turn's start and after a tool call, the turn's end is held back once for
// mail that came late, and everything is acked when the turn ends normally.
func TestSessionTurnGivesAndAcks(t *testing.T) {
	e := memEngine(t)
	a := hooked(t, e, "A")
	b := must(e.RegisterSession(t.TempDir(), "", "", ""))
	must(e.Send(b.Token, SendArgs{To: a.Name, Body: "first"}))

	r := ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessTurnStart, PromptID: "p1"})
	if !strings.Contains(r.Text, "first") || statusOf(t, e, a.ID) != StatusWorking {
		t.Fatalf("turn start: %+v %s", r, statusOf(t, e, a.ID))
	}
	if r := ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessToolBoundary, PromptID: "p1"}); r.Text != "" {
		t.Fatalf("nothing new after a tool: %+v", r)
	}
	must(e.Send(b.Token, SendArgs{To: a.Name, Body: "second"}))
	if r := ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessToolBoundary, PromptID: "other"}); r.Text != "" {
		t.Fatal("a tool call of another turn got mail")
	}
	if r := ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessToolBoundary, PromptID: "p1"}); !strings.Contains(r.Text, "second") {
		t.Fatalf("after a tool: %+v", r)
	}
	must(e.Send(b.Token, SendArgs{To: a.Name, Body: "third"}))
	r = ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessTurnEnd, PromptID: "p1", OK: true})
	if !r.Block || !strings.Contains(r.Text, "third") {
		t.Fatalf("end with new mail not held: %+v", r)
	}
	if unackedTo(e, a.ID) != 3 {
		t.Fatal("acked before the turn ended")
	}
	r = ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessTurnEnd, PromptID: "p1", OK: true})
	if r.Block || unackedTo(e, a.ID) != 0 || statusOf(t, e, a.ID) != StatusLive {
		t.Fatalf("end: %+v, %d unacked, %s", r, unackedTo(e, a.ID), statusOf(t, e, a.ID))
	}
}

// A turn that fails, is left open by Esc (idle), or is replaced by a new one is not acked: its
// mail comes again.
func TestSessionTurnNotAckedComesAgain(t *testing.T) {
	e := memEngine(t)
	a := hooked(t, e, "A")
	b := must(e.RegisterSession(t.TempDir(), "", "", ""))
	must(e.Send(b.Token, SendArgs{To: a.Name, Body: "m"}))
	for i, end := range []func(){
		func() { ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessTurnEnd, PromptID: "t0", OK: false}) },
		func() { ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessIdle}) },
		func() {}, // the next turn_start replaces it
	} {
		if r := ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessTurnStart, PromptID: "t" + string(rune('0'+i))}); !strings.Contains(r.Text, "m") {
			t.Fatalf("round %d: mail not given again: %+v", i, r)
		}
		end()
	}
	if unackedTo(e, a.ID) != 1 {
		t.Fatal("mail of an unfinished turn was acked")
	}
	// The inbox tool in a turn gives into the turn; outside a turn it acks at once.
	must(e.Send(b.Token, SendArgs{To: a.Name, Body: "pulled"}))
	if msgs := must(e.Inbox(context.Background(), a.Token, 0)); len(msgs) != 1 || msgs[0].Body != "pulled" {
		t.Fatalf("inbox in a turn: %+v", msgs)
	}
	if unackedTo(e, a.ID) != 2 {
		t.Fatal("inbox in a turn acked at once")
	}
	ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessTurnEnd, PromptID: "t2", OK: true})
	if unackedTo(e, a.ID) != 0 {
		t.Fatalf("%d unacked after the turn", unackedTo(e, a.ID))
	}
	must(e.Send(b.Token, SendArgs{To: a.Name, Body: "idle pull"}))
	if n := must(e.Heartbeat(a.Token)); n != 1 {
		t.Fatalf("unread %d", n)
	}
	must(e.Inbox(context.Background(), a.Token, 0))
	if unackedTo(e, a.ID) != 0 {
		t.Fatal("inbox outside a turn did not ack")
	}
}

func TestSessionPermissionAndEnd(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	a := hooked(t, e, "A")
	ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessTurnStart, PromptID: "p"})
	ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessPermission})
	if statusOf(t, e, a.ID) != StatusPermission {
		t.Fatal(statusOf(t, e, a.ID))
	}
	ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessToolBoundary, PromptID: "p"})
	if statusOf(t, e, a.ID) != StatusWorking {
		t.Fatal(statusOf(t, e, a.ID))
	}
	must(e.Agent(a.Token, AgentArgs{Action: "spawn", Template: "advisor", Name: "adv", Task: "?"}))
	ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessSessionEnd})
	e.mu.Lock()
	tf, _ := e.tWhere(`name=?`, "adv")
	e.mu.Unlock()
	if statusOf(t, e, a.ID) != StatusGone || tf.Status != TeamClosed {
		t.Fatalf("after session end: %s, taskforce %s", statusOf(t, e, a.ID), tf.Status)
	}
	// Starting again is the same participant, live.
	if again := hooked(t, e, "A"); again.ID != a.ID || statusOf(t, e, a.ID) != StatusLive {
		t.Fatalf("again: %+v", again)
	}
}

func TestSessionStartCardAndUnknown(t *testing.T) {
	e := memEngine(t)
	// Events of a session squad does not know, other than its start, do nothing.
	if r := ev(t, e, HarnessEventArgs{Key: "X", Event: HarnessTurnStart, PromptID: "p"}); r.Text != "" {
		t.Fatal(r)
	}
	if _, err := e.HarnessEvent(HarnessEventArgs{Event: HarnessTurnStart}); err == nil {
		t.Fatal("no key accepted")
	}
	a := hooked(t, e, "A")
	if _, err := e.HarnessEvent(HarnessEventArgs{Key: "A", Event: "dance"}); err == nil {
		t.Fatal("unknown event accepted")
	}
	if _, err := e.HarnessEvent(HarnessEventArgs{Key: "A", Event: HarnessTurnStart}); err == nil {
		t.Fatal("turn_start without prompt_id accepted")
	}
	r := ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessSessionStart, Source: "clear"})
	if !strings.Contains(r.Text, "solo session") {
		t.Fatalf("card after clear as a solo: %q", r.Text)
	}
	must(e.Agent(a.Token, AgentArgs{Action: "found", Template: "lead-peer", Name: "api"}))
	r = ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessSessionStart, Source: "compact"})
	if !strings.Contains(r.Text, `squad team "api"`) {
		t.Fatalf("card after compact in a team: %q", r.Text)
	}
	if r := ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessSessionStart, Source: "resume"}); r.Text != "" {
		t.Fatal("card on resume")
	}
}

// Mail for an idle hooked session wakes its squad mcp, at most once per WakeEvery; a working one,
// one waiting on a permission prompt, or one waiting in inbox is not woken.
func TestSessionWake(t *testing.T) {
	e := memEngine(t)
	a := hooked(t, e, "A")
	b := must(e.RegisterSession(t.TempDir(), "", "", ""))
	woke := make(chan bool, 1)
	go func() {
		w, _ := e.WaitWake(context.Background(), a.Token, 5*time.Second)
		woke <- w
	}()
	eventually(t, e, "waker registered", func() bool { return e.wakers[a.ID] != nil })
	must(e.Send(b.Token, SendArgs{To: a.Name, Body: "wake up"}))
	select {
	case w := <-woke:
		if !w {
			t.Fatal("not woken")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("mail did not wake the session")
	}
	// Woken just now: not again within WakeEvery, though the mail still waits.
	if w := must(e.WaitWake(context.Background(), a.Token, 50*time.Millisecond)); w {
		t.Fatal("woken twice in a row")
	}
	// The turn it starts takes the mail; a working session is not woken.
	ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessTurnStart, PromptID: "p"})
	e.mu.Lock()
	delete(e.lastWake, a.ID)
	e.mu.Unlock()
	must(e.Send(b.Token, SendArgs{To: a.Name, Body: "while working"}))
	if w := must(e.WaitWake(context.Background(), a.Token, 50*time.Millisecond)); w {
		t.Fatal("a working session woken")
	}
	ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessPermission})
	if w := must(e.WaitWake(context.Background(), a.Token, 50*time.Millisecond)); w {
		t.Fatal("a session at a permission prompt woken")
	}
	// The turn ends with mail left (held back too often): it wakes for a new turn.
	e.mu.Lock()
	_, _ = e.db.Exec(`UPDATE participants SET turn_blocks=? WHERE id=?`, maxBlocks, a.ID)
	e.mu.Unlock()
	ev(t, e, HarnessEventArgs{Key: "A", Event: HarnessTurnEnd, PromptID: "p", OK: true})
	if w := must(e.WaitWake(context.Background(), a.Token, time.Second)); !w {
		t.Fatal("mail left after a turn did not wake it")
	}
	// A cancelled wait leaves no waker behind.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if _, err := e.WaitWake(ctx, a.Token, 5*time.Second); err == nil {
		t.Fatal("cancelled wait returned no error")
	}
	e.mu.Lock()
	left := e.wakers[a.ID]
	e.mu.Unlock()
	if left != nil {
		t.Fatal("waker left behind")
	}
	if _, err := e.WaitWake(context.Background(), "bad", time.Millisecond); err == nil {
		t.Fatal("bad token")
	}
}
