package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bacnx/squad/internal/testdata/fake"

	"github.com/bacnx/squad/internal/driver"
	"github.com/bacnx/squad/internal/driver/local"
)

var fakeClaude string

func TestMain(m *testing.M) {
	var done func()
	fakeClaude, done = fake.Build()
	code := m.Run()
	done()
	os.Exit(code)
}

// writeProfile writes squad's Claude profile in dir: p over the defaults.
func writeProfile(t *testing.T, dir string, p local.ClaudeProfile) {
	t.Helper()
	b, _ := json.Marshal(p)
	path := local.ClaudeProfilePath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func newEngine(t *testing.T) *Engine {
	t.Helper()
	dir := t.TempDir()
	writeProfile(t, dir, local.ClaudeProfile{Cmd: fakeClaude})
	e, err := Open(Options{Dir: dir, Version: "test", Runtimes: []driver.Runtime{local.NewClaude(dir, "squad")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}

// must fails the test (by panicking) on an error.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// refused checks that err is a refusal with code or rule want.
func refused(t *testing.T, err error, want string) {
	t.Helper()
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != want && ce.Rule != want {
		t.Fatalf("want refusal %q, got %v", want, err)
	}
}

// refuses is refused for a call's results: refuses(t, "routing")(e.Send(tok, a)).
func refuses(t *testing.T, want string) func(any, error) {
	return func(_ any, err error) { t.Helper(); refused(t, err, want) }
}

func tokenOf(t *testing.T, e *Engine, name string) string {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.pWhere(`name=?`, name)
	if err != nil || p == nil {
		t.Fatalf("no participant %s: %v", name, err)
	}
	return p.Token
}

// eventually waits (up to 20s: real processes may be slow to start) for cond, which reads the
// engine under its lock.
func eventually(t *testing.T, e *Engine, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		e.mu.Lock()
		ok := cond()
		e.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func mustID(e *Engine, name string) string {
	p, _ := e.pWhere(`name=?`, name)
	if p == nil {
		return ""
	}
	return p.ID
}

// A worker waiting in the inbox tool gets its mail there at once, not on stdin (where it would
// wait for that tool call to end); once nobody waits, mail goes to its process again.
func TestWorkerInboxWaitTakesMail(t *testing.T) {
	e := newEngine(t)
	lead := must(e.RegisterSession(t.TempDir(), "", "", ""))
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "lead-peer"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "p1", Task: "t"}))
	p1 := tokenOf(t, e, "p1")
	got := make(chan []Msg, 1)
	go func() {
		msgs, _ := e.Inbox(context.Background(), p1, 10*time.Second)
		got <- msgs
	}()
	eventually(t, e, "p1 waiting", func() bool { return e.waiters[mustID(e, "p1")] != nil })
	start := time.Now()
	must(e.Send(lead.Token, SendArgs{To: "p1", Body: "the answer"}))
	select {
	case msgs := <-got:
		if len(msgs) != 1 || msgs[0].Body != "the answer" || time.Since(start) > 2*time.Second {
			t.Fatalf("inbox: %+v after %v", msgs, time.Since(start))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiting worker did not get its mail")
	}
	if n := must(e.Heartbeat(p1)); n != 0 {
		t.Fatalf("unread after inbox: %d", n)
	}

	// A cancelled wait leaves no waiter behind: the next mail goes to stdin and is acked.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _, _ = e.Inbox(ctx, p1, 10*time.Second); close(done) }()
	eventually(t, e, "p1 waiting again", func() bool { return e.waiters[mustID(e, "p1")] != nil })
	cancel()
	<-done
	must(e.Send(lead.Token, SendArgs{To: "p1", Body: "via stdin"}))
	eventually(t, e, "stdin mail acked", func() bool {
		var n int
		_ = e.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE to_id=? AND acked_at=''`, mustID(e, "p1")).Scan(&n)
		return n == 0
	})
}

// Mail to a worker in a turn is held (the inbox tool can take it) and starts the next turn.
func TestMailHeldDuringTurn(t *testing.T) {
	e := newEngine(t)
	lead := must(e.RegisterSession(t.TempDir(), "", "", ""))
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "lead-peer"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "p1", Task: "@sleep 500ms"}))
	id := mustID(e, "p1")
	eventually(t, e, "p1 working", func() bool {
		p, _ := e.byID(id)
		return p.Status == StatusWorking
	})
	must(e.Send(lead.Token, SendArgs{To: "p1", Body: "while you work"}))
	e.mu.Lock()
	var held int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE to_id=? AND delivered_at='' AND acked_at=''`, id).Scan(&held)
	e.mu.Unlock()
	if held != 1 {
		t.Fatalf("mail during a turn: %d held, want 1", held)
	}
	if n := must(e.Heartbeat(tokenOf(t, e, "p1"))); n != 1 {
		t.Fatalf("worker unread during its turn: %d, want 1", n)
	}
	eventually(t, e, "held mail delivered and acked after the turn", func() bool {
		var n int
		_ = e.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE to_id=? AND acked_at=''`, id).Scan(&n)
		return n == 0
	})
}

// A session that registers again with the same harness session id is the same participant:
// same name, token, team and waiting mail.
func TestSessionReattachesByKey(t *testing.T) {
	e := newEngine(t)
	dir := t.TempDir()
	first := must(e.RegisterSession(dir, "", "claude-session-1", "claude"))
	must(e.Agent(first.Token, AgentArgs{Action: "found", Template: "lead-peer"}))
	first = must(e.Whoami(first.Token))
	if err := e.Unregister(first.Token); err != nil {
		t.Fatal(err)
	}
	other := must(e.RegisterSession(t.TempDir(), "", "", ""))
	must(e.Send(other.Token, SendArgs{To: first.Team, Body: "for the gate"}))
	again := must(e.RegisterSession(dir, "", "claude-session-1", "claude"))
	if again.ID != first.ID || again.Token != first.Token || again.Team != first.Team || again.Role != "lead" {
		t.Fatalf("reattach: %+v, want %+v", again, first)
	}
	msgs := must(e.Inbox(context.Background(), again.Token, 0))
	if len(msgs) != 1 || msgs[0].Body != "for the gate" {
		t.Fatalf("mail after reattach: %+v", msgs)
	}
	if fresh := must(e.RegisterSession(dir, "", "claude-session-2", "claude")); fresh.ID == first.ID {
		t.Fatal("another session id got the same participant")
	}
}

// A flood of mail to a worker process never blocks the engine: delivery queues behind a worker
// that is not reading its stdin, and everything is acked in the end.
func TestFloodToWorker(t *testing.T) {
	e := newEngine(t)
	writeTemplate(t, e, "flood", `template: flood
auto_join_role: lead
roles:
  lead: {tools: [send, inbox, agent], can_spawn: [peer]}
  peer: {tools: [send, inbox]}
routing:
  - {from: lead, to: peer}
limits: {depth: 1, concurrency: 1}
`)
	lead := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "flood"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "w", Task: "t"}))
	body := strings.Repeat("x", 4000)
	for i := 0; i < 50; i++ { // 200KB: more than a pipe holds
		must(e.Send(lead.Token, SendArgs{To: "w", Body: body}))
	}
	eventually(t, e, "the flood acked", func() bool { return unackedToLocked(e, mustID(e, "w")) == 0 })
}

// Between teams only gates talk: a solo reaches a team through its name (its gate), not a member,
// and a taskforce's member stays inside it. Only the caller closes a taskforce; closing the
// caller's team closes it too.
func TestCrossTeamGate(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	sup := must(e.RegisterSession(t.TempDir(), "", "", ""))
	must(e.Agent(sup.Token, AgentArgs{Action: "found", Template: "slp", Name: "api"}))
	must(e.Agent(sup.Token, AgentArgs{Action: "spawn", Role: "lead", Name: "lead-a", Task: "t"}))
	lead := tokenOf(t, e, "lead-a")
	must(e.Agent(lead, AgentArgs{Action: "spawn", Template: "dual-lens", Name: "review", Task: "t"}))
	must(e.Agent(tokenOf(t, e, "review-chair"), AgentArgs{Action: "spawn", Role: "lens-a", Task: "t"}))
	refuses(t, "not_gate")(e.Send(tokenOf(t, e, "lens-a"), SendArgs{To: "lead-a", Body: "psst"}))

	solo := must(e.RegisterSession(t.TempDir(), "", "", ""))
	refuses(t, "not_gate")(e.Send(solo.Token, SendArgs{To: "lead-a", Body: "hi"}))
	must(e.Send(solo.Token, SendArgs{To: "api", Body: "can you look at issue 12?"}))
	if msgs := must(e.Inbox(context.Background(), sup.Token, 0)); len(msgs) != 1 || msgs[0].From != solo.Name {
		t.Fatalf("supervisor inbox: %+v", msgs)
	}

	refuses(t, "permission")(e.Agent(sup.Token, AgentArgs{Action: "close", Team: "review"}))
	must(e.Agent(sup.Token, AgentArgs{Action: "close"}))
	for _, tm := range must(e.View(0)).Teams {
		if tm.Status != TeamClosed {
			t.Fatalf("team %s is %s after close", tm.Name, tm.Status)
		}
	}
}
