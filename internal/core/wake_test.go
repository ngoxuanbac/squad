package core

import (
	"strings"
	"testing"
)

// A gone member of an open team is woken by its mail as a headless worker in its own session; the
// same mail does not wake it twice; opening the session again takes it back.
func TestGoneMemberWokenByMail(t *testing.T) {
	claude := &memRuntime{name: "claude"}
	e := memEngine(t, claude)
	gate := must(e.RegisterSession(t.TempDir(), "", "g", "claude"))
	must(e.Agent(gate.Token, AgentArgs{Action: "found", Template: "p2p", Name: "crew"}))
	dir := t.TempDir()
	m := hooked(t, e, "M") // a member: admitted by hand here (admit comes later)
	e.mu.Lock()
	tm, _ := e.tWhere(`name=?`, "crew")
	_, _ = e.db.Exec(`UPDATE participants SET team_id=?, role='peer', cwd=? WHERE id=?`, tm.ID, dir, m.ID)
	e.mu.Unlock()
	ev(t, e, HarnessEventArgs{Key: "M", Event: HarnessSessionEnd})

	must(e.Send(gate.Token, SendArgs{To: m.Name, Body: "please look at issue 7"}))
	if claude.count() != 1 {
		t.Fatalf("not woken: %d starts", claude.count())
	}
	s := claude.last()
	if !s.Resume || s.SessionRef != "M" || s.Cwd != dir || s.ParticipantID != m.ID {
		t.Fatalf("woken run: %+v", s)
	}
	e.mu.Lock()
	p, _ := e.byID(m.ID)
	e.mu.Unlock()
	if p.Kind != KindWorker || !p.WasSession || p.Status == StatusGone {
		t.Fatalf("woken member: %+v", p)
	}
	// Its run ends with the mail unread: that mail does not wake it again, newer mail does.
	must(e.Kill(m.Name))
	eventually(t, e, "stopped", func() bool { return e.runs[m.ID] == nil })
	e.mu.Lock()
	_, _ = e.db.Exec(`UPDATE mail SET acked_at='' WHERE to_id=?`, m.ID)
	_, _ = e.db.Exec(`UPDATE participants SET kind=?, status=? WHERE id=?`, KindSession, StatusGone, m.ID)
	q, _ := e.byID(m.ID)
	e.wakeGone(q)
	e.mu.Unlock()
	if claude.count() != 1 {
		t.Fatal("the same mail woke it twice")
	}
	must(e.Send(gate.Token, SendArgs{To: m.Name, Body: "and issue 8"}))
	if claude.count() != 2 {
		t.Fatal("newer mail did not wake it")
	}
	// Its person opens the session again: it is a session again, its worker stopped.
	again := hooked(t, e, "M")
	if again.ID != m.ID || again.Kind != KindSession {
		t.Fatalf("take back: %+v", again)
	}
	e.mu.Lock()
	_, running := e.runs[m.ID]
	p, _ = e.byID(m.ID)
	e.mu.Unlock()
	if running || p.WasSession || p.Status != StatusLive {
		t.Fatalf("after take back: running=%v %+v", running, p)
	}
	// What the worker read was acked by its run; mail from now on goes with the session's turns.
	if n := unackedTo(e, m.ID); n != 0 {
		t.Fatalf("%d unacked", n)
	}
	must(e.Send(gate.Token, SendArgs{To: m.Name, Body: "issue 9"}))
	if r := ev(t, e, HarnessEventArgs{Key: "M", Event: HarnessTurnStart, PromptID: "x"}); !strings.Contains(r.Text, "issue 9") {
		t.Fatalf("its mail with the next turn: %+v", r)
	}
	if claude.count() != 2 {
		t.Fatal("a live session was woken as a worker")
	}
}

// The gate, a solo, a member of a closed team, and a session without a runtime are not woken.
func TestGoneNotWoken(t *testing.T) {
	claude := &memRuntime{name: "claude"}
	e := memEngine(t, claude)
	gate := hooked(t, e, "G")
	must(e.Agent(gate.Token, AgentArgs{Action: "found", Template: "p2p", Name: "crew"}))
	other := must(e.RegisterSession(t.TempDir(), "", "", ""))
	ev(t, e, HarnessEventArgs{Key: "G", Event: HarnessSessionEnd})
	must(e.Send(other.Token, SendArgs{To: "crew", Body: "for the gone gate"}))
	solo := hooked(t, e, "S")
	ev(t, e, HarnessEventArgs{Key: "S", Event: HarnessSessionEnd})
	must(e.Send(other.Token, SendArgs{To: solo.Name, Body: "for a gone solo"}))
	gem := hooked(t, e, "N")
	e.mu.Lock()
	tm, _ := e.tWhere(`name=?`, "crew")
	_, _ = e.db.Exec(`UPDATE participants SET team_id=?, role='peer', harness='gemini' WHERE id=?`, tm.ID, gem.ID)
	e.mu.Unlock()
	ev(t, e, HarnessEventArgs{Key: "N", Event: HarnessSessionEnd})
	if claude.count() != 0 {
		t.Fatalf("%d woken", claude.count())
	}
}
