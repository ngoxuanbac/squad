package core

import (
	"fmt"
	"time"

	"github.com/bacnx/squad/internal/view"
)

// Notices: what only squad knows about a team's mail, for the person watching (Paseo, ps). They
// are mail from the engine to the reserved address notify, acked when written: nobody reads them
// as mail.
//   - reply: the gate finished a turn on team mail and sent nothing: its answer is in its session;
//   - settled: the gate sent its last message and nobody is working or has mail waiting;
//   - failed: the gate's turn on mail failed: that mail waits for new mail to be given again;
//   - gate_lost: a member left while the team's gate is not live, or there is no gate.

// notice writes one notice about team t (nil: a solo, named by gate).
func (e *Engine) notice(kind string, t *Team, gate, body string) {
	teamID := ""
	if t != nil {
		teamID = t.ID
	}
	if _, err := e.db.Exec(`INSERT INTO mail(team_id,from_id,to_id,body,created_at,kind,acked_at) VALUES(?,?,?,?,?,?,?)`,
		teamID, EngineID, AddrNotify, body, now(), kind, now()); err != nil {
		return
	}
	where := gate
	if t != nil {
		where = t.Name
	}
	e.event("notice", where, "%s: %s", kind, body)
}

// gateNotice is the notice a hooked session's turn end may write: only for a gate (or a solo)
// whose turn was given mail. ok is how the turn ended, given how much mail the turn got, left the
// mail still waiting after it.
func (e *Engine) gateNotice(p *Participant, ok bool, given, left int) {
	if p.Kind != KindSession || given == 0 || (ok && left > 0) {
		return
	}
	t, _ := e.team(p.TeamID)
	who := p.Name
	if t != nil {
		if t.GateID != p.ID {
			return
		}
		who = fmt.Sprintf("%s (gate of %s)", p.Name, t.Name)
	}
	if !ok {
		e.notice("failed", t, p.Name, who+" failed a turn on mail: the mail stays unacked until new mail arrives.")
		return
	}
	var sent int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE from_id=? AND id>(SELECT turn_mail FROM participants WHERE id=?)`, p.ID, p.ID).Scan(&sent)
	switch {
	case sent == 0:
		e.notice("reply", t, p.Name, who+" finished a turn on mail and sent nothing: its answer is in its session.")
	case t != nil && e.settled(t):
		e.notice("settled", t, p.Name, who+" sent its last message and the team is settled: nobody is working and no mail waits.")
	}
}

// settled: nobody in t is working (or at a permission prompt), and no member has mail waiting.
func (e *Engine) settled(t *Team) bool {
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM participants WHERE team_id=? AND status IN (?,?)`, t.ID, StatusWorking, StatusPermission).Scan(&n)
	if n > 0 {
		return false
	}
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM mail m JOIN participants p ON p.id=m.to_id
		WHERE p.team_id=? AND p.status!=? AND m.acked_at='' AND m.held_reason=''`, t.ID, StatusGone).Scan(&n)
	return n == 0
}

// notices are the latest n notices, newest first, as ps --view shows them.
func (e *Engine) notices(n int) []view.Notice {
	rows, err := e.db.Query(`SELECT m.created_at, m.kind, m.body, COALESCE(t.name, '') FROM mail m LEFT JOIN teams t ON t.id=m.team_id
		WHERE m.to_id=? ORDER BY m.id DESC LIMIT ?`, AddrNotify, n)
	if err != nil {
		return []view.Notice{}
	}
	defer rows.Close()
	out := []view.Notice{}
	for rows.Next() {
		var at, kind, body, team string
		if rows.Scan(&at, &kind, &body, &team) != nil {
			continue
		}
		ago := ""
		if t, err := time.Parse(time.RFC3339, at); err == nil {
			ago = shortAge(time.Since(t))
		}
		out = append(out, view.Notice{At: at, Age: ago, Where: firstNonEmpty(team, "-"), Kind: firstNonEmpty(kind, "-"), Body: body})
	}
	return out
}

// shortAge is d as 12s, 5m, 3h (under 48h), 4d.
func shortAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}
