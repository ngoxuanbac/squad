package core

import (
	"context"
	"fmt"
	"time"
)

// Harness events: what a session's hooks report (`squad hook claude <Event>`), as one standard set
// that another harness's hooks (Codex) map to as well. A session with hooks has turns: its mail is
// given with them (at a turn's start, after each tool call, and at its end, which is held back
// while new mail came), and acked only when the turn that got it ends normally. An idle one is
// woken when mail comes.
const (
	HarnessSessionStart = "session_start"
	HarnessTurnStart    = "turn_start"
	HarnessToolBoundary = "tool_boundary"
	HarnessTurnEnd      = "turn_end"
	HarnessPermission   = "permission"
	HarnessIdle         = "idle"
	HarnessSessionEnd   = "session_end"
)

// maxBlocks bounds how often one turn's end is held back to read new mail.
const maxBlocks = 3

// WakeEvery is the least time between two wakes of one session (an idle session that does not
// start its turn at once is not nudged again and again).
const WakeEvery = 15 * time.Second

// HarnessEventArgs is one hook's report.
type HarnessEventArgs struct {
	Key      string `json:"key"`     // the harness's session id (Claude's session_id)
	Harness  string `json:"harness"` // "claude"
	Event    string `json:"event"`
	PromptID string `json:"prompt_id,omitempty"` // the turn's key
	OK       bool   `json:"ok,omitempty"`        // turn_end: the turn ended normally
	Source   string `json:"source,omitempty"`    // session_start: startup | resume | clear | compact
	Cwd      string `json:"cwd,omitempty"`       // session_start: where the session runs
	Model    string `json:"model,omitempty"`
}

// HarnessEventResult is what the hook tells the harness.
type HarnessEventResult struct {
	// Text goes to the model: added to the turn (start, tool call), or as the reason a turn's end
	// is held back (Block).
	Text  string `json:"text,omitempty"`
	Block bool   `json:"block,omitempty"`
}

// HarnessEvent applies one hook report to the session it names. A session squad does not know
// is registered by its session_start; any other event of it is ignored.
func (e *Engine) HarnessEvent(a HarnessEventArgs) (HarnessEventResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var res HarnessEventResult
	if a.Key == "" {
		return res, errf("invalid", "a harness event needs the session's key")
	}
	p, err := e.pWhere(`kind=? AND session_ref=?`, KindSession, a.Key)
	if err != nil {
		return res, err
	}
	if p == nil {
		if a.Event != HarnessSessionStart || a.Cwd == "" {
			return res, nil
		}
		me, err := e.registerSession(a.Cwd, a.Model, a.Key, a.Harness)
		if err != nil {
			return res, err
		}
		if p, err = e.byID(me.ID); err != nil {
			return res, err
		}
	}
	switch a.Event {
	case HarnessSessionStart:
		status := p.Status
		if status == StatusGone {
			status = StatusLive
		}
		if _, err := e.db.Exec(`UPDATE participants SET hooked=1, status=?, last_active=? WHERE id=?`, status, now(), p.ID); err != nil {
			return res, err
		}
		if a.Source == "clear" || a.Source == "compact" {
			// The conversation was wiped or summarized, and what the session knew of squad with it.
			res.Text = "[squad] You are connected to squad.\n" + e.cardFor(p)
		} else if text := e.sessionPrompts(p); text != "" {
			res.Text = "[squad] Shared prompts for your place in squad:" + text
		}
	case HarnessTurnStart:
		if a.PromptID == "" {
			return res, errf("invalid", "turn_start needs prompt_id")
		}
		if err := e.openTurn(p, a.PromptID); err != nil {
			return res, err
		}
		if res.Text, err = e.giveTurn(p); err != nil {
			return res, err
		}
	case HarnessToolBoundary:
		if p.Status == StatusPermission { // the tool ran: the prompt was answered
			if err := e.setStatus(p, StatusWorking); err != nil {
				return res, err
			}
		}
		if p.Turn == "" || (a.PromptID != "" && a.PromptID != p.Turn) {
			return res, nil
		}
		if res.Text, err = e.giveTurn(p); err != nil {
			return res, err
		}
	case HarnessTurnEnd:
		return e.endTurn(p, a)
	case HarnessPermission:
		return res, e.setStatus(p, StatusPermission)
	case HarnessIdle:
		// Claude waits on its person: a turn left open (Esc) is over, unacked.
		if err := e.closeTurn(p, false); err != nil {
			return res, err
		}
		return res, e.setStatus(p, StatusLive)
	case HarnessSessionEnd:
		if err := e.closeTurn(p, false); err != nil {
			return res, err
		}
		if err := e.setStatus(p, StatusGone); err != nil {
			return res, err
		}
		e.closeTaskforcesOf(p.ID, "its caller "+p.Name+" is gone")
	default:
		return res, errf("invalid", "unknown harness event %q", a.Event)
	}
	return res, nil
}

func (e *Engine) setStatus(p *Participant, status string) error {
	_, err := e.db.Exec(`UPDATE participants SET status=?, last_active=? WHERE id=?`, status, now(), p.ID)
	p.Status = status
	return err
}

// openTurn makes promptID p's open turn: the one already open under that key goes on; another
// one left open is over, its mail given again.
func (e *Engine) openTurn(p *Participant, promptID string) error {
	if p.Turn == promptID {
		return e.setStatus(p, StatusWorking)
	}
	if err := e.closeTurn(p, false); err != nil {
		return err
	}
	p.Turn, p.TurnSeq, p.TurnBlocks = promptID, p.TurnSeq+1, 0
	if _, err := e.db.Exec(`UPDATE participants SET turn=?, turn_seq=?, turn_blocks=0, turn_mail=(SELECT COALESCE(MAX(id),0) FROM mail) WHERE id=?`, p.Turn, p.TurnSeq, p.ID); err != nil {
		return err
	}
	return e.setStatus(p, StatusWorking)
}

// closeTurn ends p's open turn: its mail acked when it ended normally, given again otherwise.
func (e *Engine) closeTurn(p *Participant, ok bool) error {
	if p.Turn == "" {
		return nil
	}
	if err := e.settle(p.ID, int64(p.TurnSeq), ok); err != nil {
		return err
	}
	p.Turn, p.TurnBlocks = "", 0
	_, err := e.db.Exec(`UPDATE participants SET turn='', turn_blocks=0 WHERE id=?`, p.ID)
	return err
}

// giveTurn gives p's mail not given yet to its open turn: recorded as delivered in it (acked when
// the turn ends normally) and rendered for the model; "" when there is none.
func (e *Engine) giveTurn(p *Participant) (string, error) {
	msgs, err := e.pending(p)
	if err != nil || len(msgs) == 0 {
		return "", err
	}
	if err := e.give(msgs, int64(p.TurnSeq)); err != nil {
		return "", err
	}
	return FormatMail(msgs), nil
}

// pending is p's mail neither acked, given yet nor held, oldest first, as p reads it.
func (e *Engine) pending(p *Participant) ([]Msg, error) {
	return e.readMail(p, `m.to_id=? AND m.acked_at='' AND m.delivered_at='' AND m.held_reason=''`, p.ID)
}

// endTurn is a turn's end. Ended normally with new mail since its last delivery, the end is held
// back (at most maxBlocks times) so the model reads it; otherwise its mail is acked, or given again
// when it did not end normally, and mail still waiting wakes the session for a new turn.
func (e *Engine) endTurn(p *Participant, a HarnessEventArgs) (HarnessEventResult, error) {
	var res HarnessEventResult
	open := p.Turn != "" && (a.PromptID == "" || a.PromptID == p.Turn) // the turn that ended is the open one
	ours := open && a.OK
	if ours && p.TurnBlocks < maxBlocks {
		text, err := e.giveTurn(p)
		if err != nil {
			return res, err
		}
		if text != "" {
			p.TurnBlocks++
			_, err := e.db.Exec(`UPDATE participants SET turn_blocks=? WHERE id=?`, p.TurnBlocks, p.ID)
			return HarnessEventResult{Text: text, Block: true}, err
		}
	}
	var given int
	if open {
		_ = e.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE to_id=? AND batch=? AND delivered_at!=''`, p.ID, p.TurnSeq).Scan(&given)
	}
	if err := e.closeTurn(p, ours); err != nil {
		return res, err
	}
	if err := e.setStatus(p, StatusLive); err != nil {
		return res, err
	}
	_, _ = e.db.Exec(`UPDATE participants SET last_turn_end=? WHERE id=?`, now(), p.ID)
	left := 0
	if a.OK {
		if msgs, err := e.pending(p); err == nil {
			left = len(msgs)
		}
	}
	if open {
		e.gateNotice(p, a.OK, given, left)
	}
	if a.OK {
		e.wakeSession(p)
	}
	return res, nil
}

// cardFor is what a session is told about its place: its role card in a team, else how to start.
func (e *Engine) cardFor(p *Participant) string {
	if t, _ := e.team(p.TeamID); t != nil && t.Status == TeamOpen {
		return e.roleCard(t, p)
	}
	return soloText(p.Name) + e.sharedPrompts("", "solo")
}

// soloText tells a solo session how to start.
func soloText(name string) string {
	return fmt.Sprintf("You are %s, a solo session (in no team). Found a team with agent action=found template=..., "+
		"or call a taskforce with agent action=spawn template=... task=...\n", name)
}

// sessionPrompts are the shared prompts of a session's place: its role in its team, else solo.
func (e *Engine) sessionPrompts(p *Participant) string {
	if t, _ := e.team(p.TeamID); t != nil && t.Status == TeamOpen {
		return e.sharedPrompts(t.Template, p.Role)
	}
	return e.sharedPrompts("", "solo")
}

// wakeSession tells p's squad mcp to wake it: a hooked session that is idle, with mail waiting,
// and not woken in the last WakeEvery. Callers hold e.mu.
func (e *Engine) wakeSession(p *Participant) {
	if ch, ok := e.wakers[p.ID]; ok && e.wakeDue(p) {
		close(ch)
		delete(e.wakers, p.ID)
	}
}

// wakeDue: p should be woken now.
func (e *Engine) wakeDue(p *Participant) bool {
	cur, _ := e.byID(p.ID)
	if cur == nil || !cur.Hooked || cur.Status != StatusLive || e.waiters[p.ID] != nil {
		return false
	}
	if last, ok := e.lastWake[p.ID]; ok && time.Since(last) < WakeEvery {
		return false
	}
	if msgs, err := e.pending(cur); err != nil || len(msgs) == 0 {
		return false
	}
	e.lastWake[p.ID] = time.Now()
	return true
}

// WaitWake is a hooked session's squad mcp waiting to be told to wake its session: true when it
// should (an idle session with mail), false when wait ended first.
func (e *Engine) WaitWake(ctx context.Context, token string, wait time.Duration) (bool, error) {
	deadline := time.Now().Add(wait)
	for {
		e.mu.Lock()
		p, err := e.byToken(token)
		if err != nil {
			e.mu.Unlock()
			return false, err
		}
		if e.wakeDue(p) {
			e.mu.Unlock()
			return true, nil
		}
		if time.Now().After(deadline) {
			e.mu.Unlock()
			return false, nil
		}
		ch, ok := e.wakers[p.ID]
		if !ok {
			ch = make(chan struct{})
			e.wakers[p.ID] = ch
		}
		e.mu.Unlock()
		var cancelled error
		select {
		case <-ch:
			return true, nil
		case <-time.After(min(time.Until(deadline), WakeEvery)):
		case <-ctx.Done():
			cancelled = ctx.Err()
		}
		// Not woken: take the channel out so no later wake goes to a channel nobody reads. A wake
		// closes it under e.mu, so one that came meanwhile is seen here.
		e.mu.Lock()
		select {
		case <-ch:
			e.mu.Unlock()
			return true, nil
		default:
		}
		if e.wakers[p.ID] == ch {
			delete(e.wakers, p.ID)
		}
		e.mu.Unlock()
		if cancelled != nil {
			return false, cancelled
		}
	}
}
