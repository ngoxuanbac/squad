package core

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SendArgs are the send tool's arguments.
type SendArgs struct {
	To      string `json:"to"`
	Body    string `json:"body"`
	Kind    string `json:"kind,omitempty"`     // a free label (handback, question, …); never read by squad
	ReplyTo string `json:"reply_to,omitempty"` // #N of mail the sender sent or got
	Op      string `json:"op,omitempty"`       // assign (a task to a member that reports to the sender) | replace | remove (board)
	Target  string `json:"target,omitempty"`   // board: #N of the pin to replace or remove
}

// SendResult is what a send did.
type SendResult struct {
	Seq  int64  `json:"seq"` // the mail's #N
	Held bool   `json:"held,omitempty"`
	Rule string `json:"rule,omitempty"` // the rule that holds it
}

// Text is the result as the model reads it.
func (r SendResult) Text() string {
	switch {
	case r.Held:
		return fmt.Sprintf("stored #%d but HELD by %s: not delivered until an admin releases it", r.Seq, r.Rule)
	default:
		return fmt.Sprintf("sent #%d", r.Seq)
	}
}

// Rule ids of the mail limits.
const (
	RuleRate      = "limits.messages_per_participant_per_minute"
	HoldPolicy    = "policy_hold"
	holdNoticeKey = "hold"
)

// Send is the send tool. In order: the caller's tool, the board, the
// recipient and its routing (or the gate-to-gate rule between teams), an assignment, reply_to, then
// the rate limit, which holds mail instead of refusing it; then the mail is stored, copied to the
// rule's cc roles, and handed over.
func (e *Engine) Send(token string, a SendArgs) (SendResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.byToken(token)
	if err != nil {
		return SendResult{}, err
	}
	res, err := e.send(p, a)
	if err != nil {
		var ce *Error
		if errors.As(err, &ce) && ce.Rule != "" {
			e.event("deny", e.teamName(p.TeamID), "%s -> %s: %s (%s)", p.Name, a.To, ce.Msg, ce.Rule)
		}
	}
	return res, err
}

func (e *Engine) send(p *Participant, a SendArgs) (SendResult, error) {
	a.To, a.Body, a.Kind = strings.TrimSpace(a.To), strings.TrimSpace(a.Body), strings.TrimSpace(a.Kind)
	replyTo, err := e.ref(a.ReplyTo)
	if err != nil {
		return SendResult{}, err
	}
	target, err := e.ref(a.Target)
	if err != nil {
		return SendResult{}, err
	}
	own, err := e.team(p.TeamID)
	if err != nil {
		return SendResult{}, err
	}
	if own != nil && own.Status != TeamOpen {
		return SendResult{}, errf("closed", "your team %s is closed", own.Name)
	}
	if own != nil && !own.M.HasTool(p.Role, "send") {
		return SendResult{}, denyf("permission", "tools.not_granted", "role %s has no tool %q", p.Role, "send")
	}
	if strings.EqualFold(a.To, AddrBoard) {
		return e.pin(p, own, a, replyTo, target)
	}
	if target != 0 || (a.Op != "" && a.Op != "assign") {
		return SendResult{}, errf("invalid", "target and op replace/remove are only valid for board; op assign for a member")
	}
	if strings.EqualFold(a.To, AddrNotify) {
		return SendResult{}, denyf("routing", "routing.notify", "notify is the engine's channel: agents cannot send to it")
	}
	if a.Body == "" {
		return SendResult{}, errf("invalid", "body is required")
	}
	if a.To == "" {
		return SendResult{}, errf("invalid", "send needs to")
	}
	q, cross, err := e.route(p, own, a.To)
	if err != nil {
		return SendResult{}, err
	}
	if a.Op == "assign" {
		if cross {
			return SendResult{}, errf("invalid", "op assign is for a member of your team")
		}
		if q.SpawnedBy != p.ID {
			return SendResult{}, denyf("permission", "assign.not_reports_to", "only the member's reports_to can assign it a task")
		}
	}
	if replyTo != 0 {
		if err := e.inView(p, replyTo); err != nil {
			return SendResult{}, err
		}
	}
	held, err := e.overRate(p, own)
	if err != nil {
		return SendResult{}, err
	}
	id, err := e.insertMail(p.ID, q.ID, p.TeamID, a.Body, a.Kind, a.Op, replyTo, 0, held)
	if err != nil {
		return SendResult{}, err
	}
	var copies []*Participant
	if !cross && own != nil {
		if copies, err = e.ccCopies(p, q, own, id, a, held); err != nil {
			return SendResult{}, err
		}
	}
	if a.Op == "assign" {
		_, _ = e.db.Exec(`UPDATE participants SET task=? WHERE id=?`, title(a.Body), q.ID)
	}
	e.touch(p.ID)
	if held != "" {
		e.event("held", e.teamName(p.TeamID), "%s -> %s #%d held by %s", p.Name, q.Name, id, RuleRate)
		e.holdNotice(p, id)
		return SendResult{Seq: id, Held: true, Rule: RuleRate}, nil
	}
	e.event("send", e.teamName(p.TeamID), "%s -> %s #%d: %s", p.Name, q.Name, id, oneLine(a.Body, 80))
	e.handOver(q)
	for _, c := range copies {
		e.handOver(c)
	}
	return SendResult{Seq: id}, nil
}

// ref is the mail id a #N, N or "" names (0 for "").
func (e *Engine) ref(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(s, "#"), 10, 64)
	if err != nil || n <= 0 {
		return 0, errf("invalid", "bad message ref %q: want #N", s)
	}
	var ok int
	if e.db.QueryRow(`SELECT 1 FROM mail WHERE id=?`, n).Scan(&ok) != nil {
		return 0, errf("not_found", "no message #%d", n)
	}
	return n, nil
}

// inView: mail id is one p sent or got, or a pin of p's team.
func (e *Engine) inView(p *Participant, id int64) error {
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE id=? AND (from_id=? OR to_id=? OR (to_id=? AND team_id=? AND team_id!=''))`,
		id, p.ID, p.ID, AddrBoard, p.TeamID).Scan(&n)
	if n == 0 {
		return denyf("visibility", "reply_to.not_in_view", "reply_to is not in the caller's view")
	}
	return nil
}

// insertMail stores one message and returns its #N.
func (e *Engine) insertMail(from, to, teamID, body, kind, op string, replyTo, target int64, held string) (int64, error) {
	res, err := e.db.Exec(`INSERT INTO mail(team_id,from_id,to_id,body,created_at,kind,op,reply_to,target,held_reason)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, teamID, from, to, body, now(), kind, op, replyTo, target, held)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// engineMail is mail from the engine to p (a notice, a timer, a note about a limit), handed over.
func (e *Engine) engineMail(p *Participant, kind, body string) {
	if _, err := e.insertMail(EngineID, p.ID, p.TeamID, body, kind, "", 0, 0, ""); err == nil {
		e.handOver(p)
	}
}

// route resolves `to` for p, and checks it may go: routing inside p's team, gate to gate between
// teams (cross true), and a taskforce's chair and its caller to each other.
func (e *Engine) route(p *Participant, own *Team, to string) (q *Participant, cross bool, err error) {
	isGate := own == nil || own.GateID == p.ID
	if to == "caller" {
		if own == nil || own.ParentID == "" || !isGate {
			return nil, false, errf("not_found", "you have no caller: only a taskforce's chair has one")
		}
		to = e.nameOf(own.ParentID)
	}
	if reserved[strings.ToLower(to)] {
		return nil, false, denyf("not_found", "visibility.unknown_target", "no participant or team %q in view", to)
	}
	if q, err = e.pWhere(`name=? OR id=?`, to, to); err != nil {
		return nil, false, err
	}
	if q == nil {
		t, err := e.tWhere(`name=? AND status=?`, to, TeamOpen)
		if err != nil {
			return nil, false, err
		}
		if t == nil {
			return nil, false, denyf("not_found", "visibility.unknown_target", "no participant or open team named %q (who lists them)", to)
		}
		if q, err = e.byID(t.GateID); err != nil || q == nil {
			return nil, false, denyf("not_found", "team_gate.none", "team %s has no gate", t.Name)
		}
		if t.ID == p.TeamID { // your own team's name: its gate, by your routing
			return q, false, e.routeInTeam(own, p, q)
		}
		if !isGate && t.ParentID != p.ID {
			return nil, false, denyf("not_gate", "team_gate.sender_not_gate", "only your team's gate %s sends to another team; ask %s to send it", e.nameOf(own.GateID), e.nameOf(own.GateID))
		}
		return q, true, nil
	}
	if q.ID == p.ID {
		return nil, false, errf("invalid", "you cannot write to yourself")
	}
	qt, err := e.team(q.TeamID)
	if err != nil {
		return nil, false, err
	}
	if qt != nil && qt.Status != TeamOpen {
		return nil, false, errf("closed", "%s is in team %s, which is closed", q.Name, qt.Name)
	}
	switch {
	case own != nil && q.TeamID == own.ID:
		return q, false, e.routeInTeam(own, p, q)
	case own != nil && own.ParentID == q.ID && isGate: // chair -> caller
		return q, true, nil
	case qt != nil && qt.ParentID == p.ID && qt.GateID == q.ID: // caller -> its taskforce's chair
		return q, true, nil
	case !isGate:
		return nil, false, denyf("not_gate", "team_gate.sender_not_gate", "%s is outside your team; only your team's gate %s sends to another team; ask %s to send it",
			q.Name, e.nameOf(own.GateID), e.nameOf(own.GateID))
	case qt == nil || qt.GateID == q.ID: // a solo, or another team's gate
		return q, true, nil
	default:
		return nil, false, denyf("not_gate", "team_gate.not_gate", "%s is in team %s; mail to another team goes to its gate %s", q.Name, qt.Name, e.nameOf(qt.GateID))
	}
}

// routeInTeam: the team's routing lets p's role write to q's role (the first rule matching
// decides; none denies).
func (e *Engine) routeInTeam(t *Team, p, q *Participant) error {
	if !t.M.HasTool(p.Role, "send") {
		return denyf("permission", "tools.not_granted", "role %s has no tool %q", p.Role, "send")
	}
	i := t.M.RuleIndex(p.Role, q.Role)
	if i < 0 || !t.M.Routing[i].Allowed() {
		rule := "routing.no_rule"
		if i >= 0 {
			rule = fmt.Sprintf("routing[%d]", i)
		}
		return denyf("routing", rule, "routing does not allow %s -> %s (team %s, template %s); you may write to: %s",
			p.Role, q.Role, t.Name, t.Template, orNone(t.M.Targets(p.Role)))
	}
	return nil
}

// ccMembers are the members of the rule's cc roles for mail from p to q in t, other than the
// two, each once, by role then name.
func (e *Engine) ccMembers(p, q *Participant, t *Team) ([]*Participant, error) {
	var out []*Participant
	seen := map[string]bool{p.ID: true, q.ID: true}
	for _, role := range t.M.Cc(p.Role, q.Role) {
		ms, err := e.pList(`team_id=? AND role=? ORDER BY name --`, t.ID, role)
		if err != nil {
			return nil, err
		}
		for _, m := range ms {
			if !seen[m.ID] {
				seen[m.ID] = true
				out = append(out, m)
			}
		}
	}
	return out, nil
}

// ccCopies writes one copy of mail id per cc member: same body and kind, cc_of the original, held
// with it.
func (e *Engine) ccCopies(p, q *Participant, t *Team, id int64, a SendArgs, held string) ([]*Participant, error) {
	ms, err := e.ccMembers(p, q, t)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		if _, err := e.db.Exec(`INSERT INTO mail(team_id,from_id,to_id,body,created_at,kind,cc_of,held_reason) VALUES(?,?,?,?,?,?,?,?)`,
			p.TeamID, p.ID, m.ID, a.Body, now(), a.Kind, id, held); err != nil {
			return nil, err
		}
	}
	return ms, nil
}

// overRate is HoldPolicy when p sent as many messages as its team allows in the last minute
// (copies not counted), else "".
func (e *Engine) overRate(p *Participant, own *Team) (string, error) {
	limit := soloRate
	if own != nil {
		limit = int(own.M.Limits.MessagesPerMinute)
	}
	if limit <= 0 {
		return "", nil
	}
	var n int
	since := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE from_id=? AND created_at>? AND cc_of=0`, p.ID, since).Scan(&n); err != nil {
		return "", err
	}
	if n >= limit {
		return HoldPolicy, nil
	}
	return "", nil
}

// holdNotice tells p, at most once per clock minute, that its mail is held.
func (e *Engine) holdNotice(p *Participant, id int64) {
	minute := time.Now().UTC().Truncate(time.Minute).Format(time.RFC3339)
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE from_id=? AND to_id=? AND kind=? AND created_at>=?`,
		EngineID, p.ID, holdNoticeKey, minute).Scan(&n)
	if n > 0 {
		return
	}
	e.engineMail(p, holdNoticeKey, fmt.Sprintf("Your message #%d is held by %s and was not delivered. Further messages over this limit "+
		"are held without another notice. Slow down; an admin can release it with `squad release #%d`.", id, RuleRate, id))
}

// soloRate is a solo's messages per minute: the built-in p2p's.
const soloRate = 30
