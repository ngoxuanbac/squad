package core

import (
	"context"
	"strings"
	"time"
)

// Inbox is the inbox tool: the caller's unread mail, acked as it is read. With wait it blocks
// until mail comes or the wait ends.
func (e *Engine) Inbox(ctx context.Context, token string, wait time.Duration) ([]Msg, error) {
	deadline := time.Now().Add(wait)
	for {
		e.mu.Lock()
		p, err := e.byToken(token)
		if err != nil {
			e.mu.Unlock()
			return nil, err
		}
		e.touch(p.ID)
		msgs, err := e.take(p)
		if err != nil || len(msgs) > 0 || time.Now().After(deadline) {
			e.mu.Unlock()
			return msgs, err
		}
		w := e.waiters[p.ID]
		if w == nil {
			w = &waiter{ch: make(chan struct{})}
			e.waiters[p.ID] = w
		}
		w.n++
		e.mu.Unlock()
		var werr error
		select {
		case <-w.ch:
		case <-time.After(time.Until(deadline)):
		case <-ctx.Done():
			werr = ctx.Err()
		}
		e.mu.Lock()
		if w.n--; w.n == 0 && e.waiters[p.ID] == w {
			delete(e.waiters, p.ID)
		}
		if werr != nil {
			// Nobody waits in inbox any more: a worker's mail goes to its process again.
			if p.Kind == KindWorker && e.waiters[p.ID] == nil {
				e.deliver(p)
			}
			e.mu.Unlock()
			return nil, werr
		}
		e.mu.Unlock()
	}
}

// waiter is the inbox calls of one participant waiting for mail.
type waiter struct {
	ch chan struct{}
	n  int
}

// take reads p's mail not given yet. It is acked at once, except for a hooked session in a turn:
// then it is given to that turn and acked when the turn ends normally. A worker's mail already
// handed to its process is not taken again (the process acks it when its turn completes), nor a
// hooked session's already given to its turn.
func (e *Engine) take(p *Participant) ([]Msg, error) {
	msgs, err := e.pending(p)
	if err != nil {
		return nil, err
	}
	if p.Kind == KindSession && p.Hooked && p.Turn != "" {
		return msgs, e.give(msgs, int64(p.TurnSeq))
	}
	for _, m := range msgs {
		if _, err := e.db.Exec(`UPDATE mail SET acked_at=?, gives=gives+1 WHERE id=?`, now(), m.ID); err != nil {
			return nil, err
		}
	}
	return msgs, nil
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func orNone(xs []string) string {
	if len(xs) == 0 {
		return "(none)"
	}
	return strings.Join(xs, ", ")
}
