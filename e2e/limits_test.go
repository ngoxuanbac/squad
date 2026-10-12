package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tight is a template whose limits a test reaches in a few calls.
const tight = `template: tight
auto_join_role: lead
roles:
  lead: {tools: [send, inbox, who, agent], can_spawn: [peer], can_pin: true}
  peer: {tools: [send, inbox, who, agent], can_spawn: [peer]}
routing:
  - {from: lead, to: peer}
  - {from: peer, to: lead}
  - {from: peer, to: peer}
limits: {depth: 2, concurrency: 3, messages_per_participant_per_minute: 4, max_respawn_per_hour: 2}
`

// tightTeam founds a tight team and returns its lead.
func tightTeam(t *testing.T, e *env) *agent {
	t.Helper()
	dir := filepath.Join(e.home, "templates", "tight")
	must(t, os.MkdirAll(dir, 0o700))
	must(t, os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(tight), 0o600))
	return e.founder(t.TempDir(), "tight")
}

func wantRule(t *testing.T, refusal, rule string) {
	t.Helper()
	if !strings.Contains(refusal, "refused ("+rule+")") {
		t.Fatalf("want a refusal by %s, got: %s", rule, refusal)
	}
}

func TestSpawnLimits(t *testing.T) {
	e := newEnv(t)
	lead := tightTeam(t, e)
	// p1 spawns d2 (depth 2), which may not spawn d3 (depth 3).
	lead.ok(spawn("peer", "p1", spawn("peer", "d2", spawn("peer", "d3"))))
	waitFor(t, "d2's spawn", func() bool { c := e.toolCalls("d2"); return len(c) == 1 && c[0].Done })
	wantRule(t, e.toolCalls("d2")[0].Text, "limits.depth")
	lead.ok(spawn("peer", "p2"))
	wantRule(t, lead.refused(spawn("peer", "p3")), "limits.concurrency")
}

func TestRateLimitHoldsAndReleases(t *testing.T) {
	e := newEnv(t)
	lead := tightTeam(t, e)
	lead.ok(spawn("peer", "p1")) // its brief is the 1st of 4 mails a minute
	for i := 2; i <= 4; i++ {
		lead.ok(send("p1", fmt.Sprintf("m%d", i)))
	}
	held := lead.ok(send("p1", "m5"))
	id := regexp.MustCompile(`stored #(\d+) but HELD`).FindStringSubmatch(held)
	if id == nil {
		t.Fatalf("5th mail in a minute: %s", held)
	}
	lead.mail("held") // the sender is told
	tail := func() string { return e.squad("tail", "p1", "-n", "1000", "--no-start") }
	waitFor(t, "m4 delivered", func() bool { return strings.Contains(tail(), "m4") })
	if strings.Contains(tail(), "m5") {
		t.Fatal("held mail was delivered")
	}
	e.squad("release", "#"+id[1])
	waitFor(t, "m5 delivered", func() bool { return strings.Contains(tail(), "m5") })
}

func TestCrashLoopIsParked(t *testing.T) {
	e := newEnv(t)
	lead := tightTeam(t, e)
	lead.ok(spawn("peer", "p1", "@exit 1")) // every turn dies, the task comes again with each mail
	for i := 0; i < 2; i++ {
		e.waitStatus("p1", "stopped")
		lead.ok(send("p1", "again"))
		e.waitStatus("p1", "idle", "working", "stopped")
	}
	e.waitStatus("p1", "stopped")
	lead.ok(send("p1", "again"))
	e.waitStatus("p1", "parked")
	lead.mail("parked (limits.max_respawn_per_hour)")
}

func TestBoard(t *testing.T) {
	e := newEnv(t)
	lead := tightTeam(t, e)
	lead.ok(spawn("peer", "p1", send("board", "mine")))
	waitFor(t, "p1's pin", func() bool { c := e.toolCalls("p1"); return len(c) == 1 && c[0].Done })
	wantRule(t, e.toolCalls("p1")[0].Text, "can_pin")

	first := lead.ok(send("board", "pin 1"))
	for i := 2; i <= 20; i++ {
		lead.ok(send("board", fmt.Sprintf("pin %d", i)))
	}
	wantRule(t, lead.refused(send("board", "pin 21")), "board.full")
	pin := regexp.MustCompile(`#\d+`).FindString(first)
	lead.ok(do("send", map[string]any{"to": "board", "op": "replace", "target": pin, "body": "pin one"}))
	board := lead.ok(do("inbox", map[string]any{"view": "board"}))
	if !strings.Contains(board, "pin one") || strings.Contains(board, "pin 1\n") || !strings.Contains(board, "pin 20") {
		t.Fatalf("board:\n%s", board)
	}
}
