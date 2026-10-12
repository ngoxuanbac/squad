package core

// Delivery: getting mail to a worker's process, or to whoever waits in the inbox tool.

// handOver gets q's new mail to it: whoever waits in the inbox tool takes it there (a message
// written to a worker's stdin would wait for that tool call to end); a gone session is woken as a
// worker, a hooked one woken; any other worker gets it on stdin.
func (e *Engine) handOver(q *Participant) {
	if _, waiting := e.waiters[q.ID]; waiting {
		e.wake(q.ID)
		return
	}
	if q.Kind == KindSession {
		if q.Status == StatusGone {
			e.wakeGone(q)
			return
		}
		e.wakeSession(q) // a hooked session in a turn gets it at its next tool call or its end
		return
	}
	e.deliver(q)
}

// deliver hands q's undelivered mail to its process as one batch, starting (resuming) the worker
// first when it is stopped and its team is open. A worker in a turn is not written to: a message
// on stdin would wait in Claude's queue where the inbox tool cannot reach it. Its tool results say
// mail is waiting, and what it has not taken comes as a new turn when the current one ends.
func (e *Engine) deliver(q *Participant) {
	if q.Kind != KindWorker || e.closing {
		return
	}
	if cur, _ := e.byID(q.ID); cur != nil && cur.Status == StatusWorking {
		return
	}
	run := e.runs[q.ID]
	if run == nil {
		t, _ := e.team(q.TeamID)
		if t == nil || t.Status != TeamOpen || q.Status == StatusParked {
			return
		}
		// One mail resumes it once: a worker that stops again with it unread waits for newer mail.
		var newest int64
		if e.db.QueryRow(`SELECT COALESCE(MAX(id),0) FROM mail WHERE to_id=? AND acked_at='' AND held_reason=''`, q.ID).Scan(&newest) != nil || newest <= q.WokeMail {
			return
		}
		_, _ = e.db.Exec(`UPDATE participants SET woke_mail=? WHERE id=?`, newest, q.ID)
		q.WokeMail = newest
		if err := e.startWorker(q, true); err != nil {
			e.event("deny", t.Name, "could not resume %s for its mail: %v", q.Name, err)
		}
		return // startWorker delivers once the process runs
	}
	msgs, err := e.pending(q)
	if err != nil || len(msgs) == 0 {
		return
	}
	batch := msgs[len(msgs)-1].ID
	if err := run.Deliver(batch, FormatMail(msgs)); err != nil {
		e.event("deny", e.teamName(q.TeamID), "delivery to %s failed: %v", q.Name, err)
		return
	}
	_ = e.give(msgs, batch)
}

// give records msgs as handed over in batch: acked when the batch ends normally (settle).
func (e *Engine) give(msgs []Msg, batch int64) error {
	for _, m := range msgs {
		if _, err := e.db.Exec(`UPDATE mail SET delivered_at=?, batch=?, gives=gives+1 WHERE id=?`, now(), batch, m.ID); err != nil {
			return err
		}
	}
	return nil
}

// settle ends batch of id's mail: acked when it ended normally, else given again.
func (e *Engine) settle(id string, batch int64, ok bool) error {
	q, args := `UPDATE mail SET delivered_at='', batch=0 WHERE to_id=? AND batch=? AND acked_at=''`, []any{id, batch}
	if ok {
		q, args = `UPDATE mail SET acked_at=? WHERE to_id=? AND batch=? AND acked_at=''`, []any{now(), id, batch}
	}
	_, err := e.db.Exec(q, args...)
	return err
}

// regive makes id's mail not acked come again.
func (e *Engine) regive(id string) {
	_, _ = e.db.Exec(`UPDATE mail SET delivered_at='', batch=0 WHERE to_id=? AND acked_at=''`, id)
}
