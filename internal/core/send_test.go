package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// leadPeer is a lead-peer team: a lead session (the gate) with two worker peers.
func leadPeer(t *testing.T, e *Engine) (lead Me, p1, p2 string) {
	t.Helper()
	lead = must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "lead-peer", Name: "api"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "p1", Task: "## Fix **pkg/a**\nmore"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "p2", Task: "fix pkg/b"}))
	return lead, tokenOf(t, e, "p1"), tokenOf(t, e, "p2")
}

func lastMailTo(t *testing.T, e *Engine, name string) Msg {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	p, _ := e.pWhere(`name=?`, name)
	ms, err := e.readMail(p, `m.to_id=?`, p.ID)
	if err != nil || len(ms) == 0 {
		t.Fatalf("no mail to %s: %v", name, err)
	}
	return ms[len(ms)-1]
}

func TestMailModel(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	lead, p1, p2 := leadPeer(t, e)

	// The spawn task is an assignment: its title is the member's task.
	e.mu.Lock()
	q, _ := e.pWhere(`name=?`, "p1")
	e.mu.Unlock()
	if q.Task != "Fix pkg/a" {
		t.Fatalf("task title %q", q.Task)
	}

	r := must(e.Send(p1, SendArgs{To: lead.Name, Body: "done with pkg/a", Kind: "handback"}))
	if r.Seq == 0 || r.Text() != fmt.Sprintf("sent #%d", r.Seq) {
		t.Fatalf("result %+v %q", r, r.Text())
	}
	msgs := must(e.Inbox(context.Background(), lead.Token, 0))
	if len(msgs) != 1 || msgs[0].ID != r.Seq || msgs[0].Label != "p1 (peer, reports to you)" || msgs[0].Kind != "handback" {
		t.Fatalf("inbox: %+v", msgs)
	}
	text := FormatMail(msgs)
	for _, want := range []string{"[squad] 1 new message:", fmt.Sprintf(`<message id="#%d"`, r.Seq), `from="p1 (peer, reports to you)"`,
		`sender="p1"`, `kind="handback"`, "done with pkg/a", "</message>", "reply_to=<its #N>"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendering lacks %q:\n%s", want, text)
		}
	}

	// A reply names the mail it answers; the member sees its lead as the one it reports to.
	rr := must(e.Send(lead.Token, SendArgs{To: "p1", Body: "thanks", ReplyTo: fmt.Sprint(r.Seq)}))
	got := lastMailTo(t, e, "p1")
	if got.ID != rr.Seq || got.ReplyTo != r.Seq || got.Label != lead.Name+" (lead, you report to them)" {
		t.Fatalf("reply as p1 sees it: %+v", got)
	}
	if !strings.Contains(FormatMail([]Msg{got}), fmt.Sprintf(`reply_to="#%d"`, r.Seq)) {
		t.Fatal("reply_to not rendered")
	}
	// reply_to must be mail the sender sent or got.
	_, err := e.Send(p2, SendArgs{To: lead.Name, Body: "me too", ReplyTo: fmt.Sprintf("#%d", r.Seq)})
	refusedRule(t, err, "reply_to.not_in_view")
	for ref, code := range map[string]string{"#0": "invalid", "abc": "invalid", "#999999": "not_found"} {
		_, err := e.Send(p1, SendArgs{To: lead.Name, Body: "x", ReplyTo: ref})
		refused(t, err, code)
	}

	// A duplicate client_msg_id returns the original, writes nothing.
	d1 := must(e.Send(p1, SendArgs{To: lead.Name, Body: "once", ClientMsgID: "k1"}))
	d2 := must(e.Send(p1, SendArgs{To: lead.Name, Body: "once", ClientMsgID: "k1"}))
	if !d2.Duplicate || d2.Seq != d1.Seq || d2.Text() != fmt.Sprintf("duplicate of #%d", d1.Seq) {
		t.Fatalf("duplicate: %+v", d2)
	}
	if n := len(must(e.Inbox(context.Background(), lead.Token, 0))); n != 1 {
		t.Fatalf("duplicate delivered: %d", n)
	}

	// Assign: only the member's lead; never across teams.
	must(e.Send(lead.Token, SendArgs{To: "p2", Body: "Also look at pkg/c", Op: "assign"}))
	e.mu.Lock()
	q, _ = e.pWhere(`name=?`, "p2")
	e.mu.Unlock()
	if q.Task != "Also look at pkg/c" {
		t.Fatalf("assigned task %q", q.Task)
	}
	_, err = e.Send(p1, SendArgs{To: "p2", Body: "do this", Op: "assign"})
	refusedRule(t, err, "assign.not_reports_to")
	solo := must(e.RegisterSession(t.TempDir(), "", "", ""))
	_, err = e.Send(solo.Token, SendArgs{To: lead.Name, Body: "x", Op: "assign"})
	refused(t, err, "invalid")

	// Shapes and reserved addresses.
	for _, a := range []SendArgs{{To: "p2", Body: "x", Target: "#1"}, {To: "p2", Body: "x", Op: "replace"}, {To: "p2"}, {Body: "x"}} {
		_, err := e.Send(lead.Token, a)
		refused(t, err, "invalid")
	}
	_, err = e.Send(lead.Token, SendArgs{To: "notify", Body: "x"})
	refusedRule(t, err, "routing.notify")
	_, err = e.Send(lead.Token, SendArgs{To: "engine", Body: "x"})
	refusedRule(t, err, "visibility.unknown_target")
	_, err = e.Send(lead.Token, SendArgs{To: lead.Name, Body: "x"})
	refused(t, err, "invalid")

	// Labels: a solo, another team, the engine.
	if _, l := e.label(nil, solo.ID); l != solo.Name+" (solo)" {
		t.Fatal(l)
	}
	if _, l := e.label(nil, mustID(e, "p1")); l != "p1 (peer, team api)" {
		t.Fatal(l)
	}
	if n, l := e.label(nil, EngineID); n != "engine" || l != "engine" {
		t.Fatal(l)
	}
	if _, l := e.label(nil, "p_gone"); l != "p_gone (solo)" {
		t.Fatal(l)
	}
}

func refusedRule(t *testing.T, err error, rule string) {
	t.Helper()
	ce, ok := err.(*Error)
	if !ok || ce.Rule != rule {
		t.Fatalf("want refusal by %s, got %v", rule, err)
	}
}

const routingTemplate = `template: rt
auto_join_role: lead
roles:
  lead: {tools: [send, inbox, who, agent], can_spawn: [dev, qa], can_pin: true}
  dev: {tools: [send, inbox]}
  qa: {tools: [send, inbox]}
routing:
  - {from: lead, to: dev}
  - {from: lead, to: qa}
  - {from: dev, to: lead, cc: [qa, dev]}
  - {from: dev, to: dev, allow: false}
  - {from: dev, to: dev}
limits: {depth: 2, concurrency: 10, messages_per_participant_per_minute: 2}
`

func TestRoutingRulesCcAndHold(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	writeTemplate(t, e, "rt", routingTemplate)
	lead := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "rt"}))
	for _, n := range []string{"d1", "d2"} {
		must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "dev", Name: n, Task: "t"}))
	}
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "qa", Name: "q1", Task: "t"}))
	d1 := tokenOf(t, e, "d1")

	// The first matching rule decides: an allow:false before an allow denies with its index.
	_, err := e.Send(d1, SendArgs{To: "d2", Body: "x"})
	refusedRule(t, err, "routing[3]")
	_, err = e.Send(tokenOf(t, e, "q1"), SendArgs{To: lead.Name, Body: "x"})
	refusedRule(t, err, "routing.no_rule")

	// cc: every other member of the rule's cc roles gets a copy, marked as one.
	r := must(e.Send(d1, SendArgs{To: lead.Name, Body: "handback", Kind: "handback"}))
	cp := lastMailTo(t, e, "q1")
	if cp.CcOf != r.Seq || cp.Body != "handback" || cp.Kind != "handback" || cp.CcOfTo != lead.Name+" (lead, you report to them)" {
		t.Fatalf("q1's copy: %+v", cp)
	}
	if !strings.Contains(FormatMail([]Msg{cp}), fmt.Sprintf(`cc_of="#%d"`, r.Seq)) {
		t.Fatal("cc not rendered")
	}
	if c := lastMailTo(t, e, "d2"); c.CcOf != r.Seq {
		t.Fatalf("d2's copy: %+v", c)
	}
	e.mu.Lock()
	var copiesToSender int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE cc_of=? AND to_id=?`, r.Seq, mustID(e, "d1")).Scan(&copiesToSender)
	e.mu.Unlock()
	if copiesToSender != 0 {
		t.Fatal("the sender got a copy")
	}

	// Rate: d1 sent 1 (copies do not count), so one more goes, the next is held, and d1 is told once.
	must(e.Send(d1, SendArgs{To: lead.Name, Body: "two"}))
	h := must(e.Send(d1, SendArgs{To: lead.Name, Body: "three"}))
	if !h.Held || h.Rule != RuleRate || !strings.Contains(h.Text(), "HELD by "+RuleRate) {
		t.Fatalf("held: %+v", h)
	}
	h2 := must(e.Send(d1, SendArgs{To: lead.Name, Body: "four"}))
	e.mu.Lock()
	var notes, heldCopies int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE from_id=? AND to_id=? AND kind=?`, EngineID, mustID(e, "d1"), holdNoticeKey).Scan(&notes)
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE cc_of IN (?,?) AND held_reason!=''`, h.Seq, h2.Seq).Scan(&heldCopies)
	e.mu.Unlock()
	if notes != 1 || heldCopies != 4 {
		t.Fatalf("%d hold notices, %d held copies", notes, heldCopies)
	}
	// Held mail is not given; a duplicate of it reports the hold.
	for _, m := range must(e.Inbox(context.Background(), lead.Token, 0)) {
		if m.Body == "three" || m.Body == "four" {
			t.Fatal("held mail given")
		}
	}
	// Release gives it, with its copies.
	if out := must(e.Release(fmt.Sprintf("#%d", h.Seq))); out != fmt.Sprintf("released #%d", h.Seq) {
		t.Fatal(out)
	}
	if msgs := must(e.Inbox(context.Background(), lead.Token, 0)); len(msgs) != 1 || msgs[0].Body != "three" {
		t.Fatalf("after release: %+v", msgs)
	}
	_, err = e.Release(fmt.Sprintf("#%d", h.Seq))
	refused(t, err, "invalid") // not held any more
	_, err = e.Release("#999999")
	refused(t, err, "not_found")
	_, err = e.Release("")
	refused(t, err, "invalid")
	must(e.TeamDown(lead.Name))
	_, err = e.Release(fmt.Sprintf("#%d", h2.Seq))
	refusedRule(t, err, "team.closed")
}

func TestBoard(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	writeTemplate(t, e, "rt", routingTemplate)
	lead := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	solo := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	_, err := e.Send(solo.Token, SendArgs{To: "board", Body: "x"})
	refusedRule(t, err, "can_pin")
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "rt"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "dev", Name: "d1", Task: "t"}))
	_, err = e.Send(tokenOf(t, e, "d1"), SendArgs{To: "board", Body: "x"})
	refusedRule(t, err, "can_pin")

	if text := must(e.BoardText(lead.Token)); text != "Nothing in this view." {
		t.Fatal(text)
	}
	first := must(e.Send(lead.Token, SendArgs{To: "Board", Body: "Plan: ship on Friday"}))
	teamID := mustTeam(e, lead.ID)
	for i := 1; i < BoardLimit; i++ {
		e.mu.Lock() // pins past the rate limit, written as the board would be
		_, _ = e.db.Exec(`INSERT INTO mail(team_id,from_id,to_id,body,created_at) VALUES(?,?,?,?,?)`, teamID, lead.ID, AddrBoard, fmt.Sprint("pin ", i), "2000-01-01T00:00:00Z")
		e.mu.Unlock()
	}
	_, err = e.Send(lead.Token, SendArgs{To: "board", Body: "one too many"})
	refusedRule(t, err, "board.full")
	// replace keeps the count; remove frees a place.
	rep := must(e.Send(lead.Token, SendArgs{To: "board", Body: "Plan: ship on Monday", Op: "replace", Target: fmt.Sprintf("#%d", first.Seq)}))
	_, err = e.Send(lead.Token, SendArgs{To: "board", Op: "remove", Target: fmt.Sprintf("#%d", first.Seq)})
	refusedRule(t, err, "board.target_not_live") // replaced already
	must(e.Send(lead.Token, SendArgs{To: "board", Op: "remove", Target: fmt.Sprintf("#%d", rep.Seq)}))
	must(e.Send(lead.Token, SendArgs{To: "board", Body: "now it fits"}))
	text := must(e.BoardText(lead.Token))
	if !strings.Contains(text, fmt.Sprintf("view board, %d messages, nothing marked read", BoardLimit)) || strings.Contains(text, "Monday") ||
		!strings.Contains(text, "now it fits") {
		t.Fatalf("board:\n%s", text[:200])
	}
	for _, a := range []SendArgs{
		{To: "board", Body: "x", Target: "#1"},
		{To: "board", Op: "replace", Target: fmt.Sprintf("#%d", first.Seq)},
		{To: "board", Op: "assign", Body: "x"},
		{To: "board"},
	} {
		_, err := e.Send(lead.Token, a)
		refused(t, err, "invalid")
	}
	// A pin wakes nobody and is in the team's view for reply_to.
	r := must(e.Send(lead.Token, SendArgs{To: "d1", Body: "see the pin", ReplyTo: fmt.Sprintf("#%d", rep.Seq)}))
	if r.Seq == 0 {
		t.Fatal("reply to a pin")
	}
	_, err = e.BoardText("bad")
	refused(t, err, "auth")
}

func mustTeam(e *Engine, id string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, _ := e.byID(id)
	return p.TeamID
}

func TestRenderTimes(t *testing.T) {
	now := time.Date(2026, 10, 11, 15, 0, 0, 0, time.Local)
	for d, want := range map[time.Duration]string{30 * time.Second: "", 4 * time.Minute: "4m", 2 * time.Hour: "2h",
		2*time.Hour + 5*time.Minute: "2h5m", 72 * time.Hour: "3d", 74 * time.Hour: "3d2h"} {
		if got := age(d); got != want {
			t.Errorf("age(%v) = %q, want %q", d, got, want)
		}
	}
	at, a := clock(now.Add(-2*time.Minute).UTC().Format(time.RFC3339), now)
	if at != "14:58:00" || a != "2m" {
		t.Fatalf("today: %q %q", at, a)
	}
	at, _ = clock(now.Add(-48*time.Hour).UTC().Format(time.RFC3339), now)
	if at != "2026-10-09 15:00" {
		t.Fatalf("other day: %q", at)
	}
	if at, a := clock("garbage", now); at != "garbage" || a != "" {
		t.Fatal(at)
	}
	if title("\n\n> **Bold** `code` here") != "Bold code here" || len([]rune(title(strings.Repeat("x", 150)))) != 101 || title("  ") != "" {
		t.Fatal("title")
	}
}
