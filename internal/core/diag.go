package core

import (
	"fmt"
	"strings"
	"time"
)

// Diagnostics for the person running squad: why a send would be refused, the event log, and what
// looks stuck.

// WhyLine is one check of a send, as `squad why` prints it.
type WhyLine struct {
	Check  string `json:"check"`
	Result string `json:"result"` // pass | deny | hold | info
	Rule   string `json:"rule,omitempty"`
	Detail string `json:"detail"`
}

// Why runs the checks of a send from `from` (a name or id) to `to`, writing nothing. The verdict
// is allow, deny or hold.
func (e *Engine) Why(from, to string) ([]WhyLine, string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.target(from)
	if err != nil {
		return nil, "", err
	}
	own, _ := e.team(p.TeamID)
	lines := []WhyLine{{Check: "caller", Result: "pass", Detail: fmt.Sprintf("%s is %s, role %s, %s", p.Name, p.ID, firstNonEmpty(p.Role, "(solo)"), p.Status)}}
	deny := func(check string, err error) ([]WhyLine, string, error) {
		ce, ok := err.(*Error)
		if !ok {
			return lines, "", err
		}
		lines = append(lines, WhyLine{Check: check, Result: "deny", Rule: firstNonEmpty(ce.Rule, ce.Code), Detail: ce.Msg})
		return lines, "deny", nil
	}
	if own != nil {
		if own.Status != TeamOpen {
			return deny("team", errf("closed", "team %s is closed", own.Name))
		}
		if !own.M.HasTool(p.Role, "send") {
			return deny("permission", denyf("permission", "tools.not_granted", "role %s has no tool %q", p.Role, "send"))
		}
		lines = append(lines, WhyLine{Check: "permission", Result: "pass", Rule: "tools", Detail: fmt.Sprintf("role %s has tool send", p.Role)})
	}
	if strings.EqualFold(to, AddrBoard) {
		if own == nil || !own.M.Roles[p.Role].CanPin {
			return deny("board", denyf("permission", "can_pin", "role %s cannot pin", firstNonEmpty(p.Role, "solo")))
		}
		lines = append(lines, WhyLine{Check: "board", Result: "pass", Rule: "can_pin", Detail: fmt.Sprintf("role %s can pin", p.Role)})
		return lines, "allow", nil
	}
	if strings.EqualFold(to, AddrNotify) {
		return deny("routing", denyf("routing", "routing.notify", "notify is the engine's channel: agents cannot send to it"))
	}
	q, cross, err := e.route(p, own, to)
	if err != nil {
		return deny("route", err)
	}
	if cross {
		lines = append(lines, WhyLine{Check: "team_gate", Result: "pass", Detail: fmt.Sprintf("%s reaches %s gate to gate", p.Name, q.Name)})
	} else {
		lines = append(lines, WhyLine{Check: "visibility", Result: "pass", Detail: fmt.Sprintf("%s is %s, role %s, in team %s", q.Name, q.ID, q.Role, e.teamName(q.TeamID))})
		i := own.M.RuleIndex(p.Role, q.Role)
		lines = append(lines, WhyLine{Check: "routing", Result: "pass", Rule: fmt.Sprintf("routing[%d]", i), Detail: fmt.Sprintf("allows %s -> %s", p.Role, q.Role)})
	}
	verdict := "allow"
	if held, _ := e.overRate(p, own); held != "" {
		lines = append(lines, WhyLine{Check: "limits", Result: "hold", Rule: RuleRate, Detail: "over the limit now: stored but held until an admin releases it"})
		verdict = "hold"
	} else {
		lines = append(lines, WhyLine{Check: "limits", Result: "pass", Detail: "no mail limit is reached"})
	}
	if !cross && own != nil {
		var names []string
		ms, _ := e.ccMembers(p, q, own)
		for _, m := range ms {
			names = append(names, m.Name)
		}
		if len(names) > 0 {
			lines = append(lines, WhyLine{Check: "cc", Result: "info", Detail: "copies to " + strings.Join(names, ", ")})
		}
	}
	return lines, verdict, nil
}

// LogEvent is one event of the log.
type LogEvent struct {
	Seq  int64  `json:"seq"`
	At   string `json:"at"`
	Kind string `json:"kind"`
	Team string `json:"team"`
	Text string `json:"text"`
}

// Log is the events after seq (team "" any), oldest first, at most limit (0: 200).
func (e *Engine) Log(after int64, team string, limit int) ([]LogEvent, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if limit <= 0 {
		limit = 200
	}
	q, args := `SELECT id, at, kind, team, text FROM events WHERE id>?`, []any{after}
	if team != "" {
		q, args = q+` AND team=?`, append(args, team)
	}
	rows, err := e.db.Query(q+` ORDER BY id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogEvent{}
	for rows.Next() {
		var ev LogEvent
		if err := rows.Scan(&ev.Seq, &ev.At, &ev.Kind, &ev.Team, &ev.Text); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// Finding is something doctor sees that looks stuck.
type Finding struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Who    string `json:"who,omitempty"`
}

// DoctorStale is how long a turn may go on before doctor reports it.
const DoctorStale = 30 * time.Minute

// Doctor lists what looks stuck:
//   - stuck_turn: a member working for longer than DoctorStale with no turn end;
//   - gone_unacked: a gone member with mail waiting;
//   - held: mail held by a limit;
//   - no_process: a worker live in squad's records but with no process;
//   - parked: a worker parked by its respawn limit.
func (e *Engine) Doctor() ([]Finding, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []Finding{}
	cut := time.Now().Add(-DoctorStale).UTC().Format(time.RFC3339)
	ps, err := e.pList(`status=? AND state_since<? AND last_turn_end<?`, StatusWorking, cut, cut)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		out = append(out, Finding{Kind: "stuck_turn", Who: p.Name, Detail: fmt.Sprintf("working since %s with no turn end", p.StateSince)})
	}
	gone, _ := e.pList(`status=?`, StatusGone)
	for _, p := range gone {
		if n, _ := e.unread(p.ID); n > 0 {
			out = append(out, Finding{Kind: "gone_unacked", Who: p.Name, Detail: fmt.Sprintf("gone with %d unacked message(s)", n)})
		}
	}
	var held int
	var oldest int64
	_ = e.db.QueryRow(`SELECT COUNT(*), COALESCE(MIN(id),0) FROM mail WHERE held_reason!='' AND cc_of=0`).Scan(&held, &oldest)
	if held > 0 {
		out = append(out, Finding{Kind: "held", Detail: fmt.Sprintf("%d message(s) held by %s, oldest #%d (squad release #%d)", held, RuleRate, oldest, oldest)})
	}
	live, _ := e.pList(`kind=? AND status IN (?,?)`, KindWorker, StatusLive, StatusWorking)
	for _, p := range live {
		if _, ok := e.runs[p.ID]; !ok {
			out = append(out, Finding{Kind: "no_process", Who: p.Name, Detail: fmt.Sprintf("%s but no process", p.Status)})
		}
	}
	parked, _ := e.pList(`status=?`, StatusParked)
	for _, p := range parked {
		out = append(out, Finding{Kind: "parked", Who: p.Name, Detail: "parked since " + p.StateSince + " (squad resume once the hour allows)"})
	}
	return out, nil
}
