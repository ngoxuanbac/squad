package core

import (
	"fmt"
	"time"
)

// The board: a team's pins. A pin is mail to "board" (team = the pinner's). A live pin is one no
// later replace or remove targets; a replace is a new live pin in place of its target. Nobody is
// woken by a pin: members read the board with inbox view=board.

// BoardLimit is how many live pins a team's board holds.
const BoardLimit = 20

const livePins = `m.to_id='board' AND m.team_id=? AND m.op IN ('', 'replace')
	AND NOT EXISTS (SELECT 1 FROM mail x WHERE x.to_id='board' AND x.target=m.id AND x.op IN ('replace', 'remove'))`

// pin is a send to the board: a new pin (op ""), a replace or a remove of a live pin of p's team.
func (e *Engine) pin(p *Participant, own *Team, a SendArgs, replyTo, target int64) (SendResult, error) {
	if own == nil || !own.M.Roles[p.Role].CanPin {
		role := "solo"
		if own != nil {
			role = p.Role
		}
		return SendResult{}, denyf("permission", "can_pin", "role %s cannot pin", role)
	}
	switch a.Op {
	case "":
		if target != 0 {
			return SendResult{}, errf("invalid", "target requires op replace or remove")
		}
		if a.Body == "" {
			return SendResult{}, errf("invalid", "body is required")
		}
		var n int
		if err := e.db.QueryRow(`SELECT COUNT(*) FROM mail m WHERE `+livePins, own.ID).Scan(&n); err != nil {
			return SendResult{}, err
		}
		if n >= BoardLimit {
			return SendResult{}, denyf("limit", "board.full", "board is full (%d pins): replace or remove one", n)
		}
	case "replace", "remove":
		if a.Op == "replace" && a.Body == "" {
			return SendResult{}, errf("invalid", "body is required")
		}
		var n int
		if err := e.db.QueryRow(`SELECT COUNT(*) FROM mail m WHERE m.id=? AND `+livePins, target, own.ID).Scan(&n); err != nil {
			return SendResult{}, err
		}
		if n == 0 {
			return SendResult{}, denyf("target", "board.target_not_live", "target is not a live pin of this team")
		}
	default:
		return SendResult{}, errf("invalid", "unknown board op %q", a.Op)
	}
	if replyTo != 0 {
		if err := e.inView(p, replyTo); err != nil {
			return SendResult{}, err
		}
	}
	id, err := e.insertMail(p.ID, AddrBoard, p.TeamID, a.Body, a.Kind, a.Op, replyTo, target, a.ClientMsgID, "")
	if err != nil {
		return SendResult{}, err
	}
	e.touch(p.ID)
	verb := map[string]string{"": "pinned", "replace": "replaced a pin with", "remove": "removed pin"}[a.Op]
	e.event("pin", own.Name, "%s %s #%d", p.Name, verb, map[bool]int64{true: target, false: id}[a.Op == "remove"])
	return SendResult{Seq: id}, nil
}

// board is the live pins of p's team (at most BoardLimit), oldest first.
func (e *Engine) board(p *Participant) ([]Msg, error) {
	if p.TeamID == "" {
		return nil, nil
	}
	return e.readMail(p, livePins, p.TeamID)
}

// BoardText is inbox view=board: read-only, nothing marked read.
func (e *Engine) BoardText(token string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.byToken(token)
	if err != nil {
		return "", err
	}
	if t, _ := e.team(p.TeamID); t != nil && !t.M.HasTool(p.Role, "inbox") {
		return "", denyf("permission", "tools.not_granted", "role %s has no tool %q", p.Role, "inbox")
	}
	pins, err := e.board(p)
	if err != nil || len(pins) == 0 {
		return "Nothing in this view.", err
	}
	return formatMail(pins, fmt.Sprintf("view board, %d message%s, nothing marked read", len(pins), plural(len(pins))), time.Now()), nil
}
