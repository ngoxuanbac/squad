package core

// Mail wakes a gone member: new mail for a gone session of an open team, other than the team's
// gate, resumes that session as a headless worker on its harness (the same Claude session, in
// its directory), and the mail comes with the run. The gate is never woken: a gone gate's mail
// waits for its person. A solo, a member of a closed team, and a session squad has no session id
// or runtime for are never woken. One mail wakes it once: a member that stops again with that mail
// unread is woken again only by newer mail. Opening the session again takes it back (takeBack).

// wakeGone resumes q, a gone session, as a worker when its newest mail may wake it. Callers hold
// e.mu.
func (e *Engine) wakeGone(q *Participant) {
	t, _ := e.team(q.TeamID)
	if t == nil || t.Status != TeamOpen || t.GateID == q.ID || q.SessionRef == "" {
		return
	}
	if _, ok := e.runtimes[q.Harness]; !ok {
		return
	}
	var newest int64
	if err := e.db.QueryRow(`SELECT COALESCE(MAX(id),0) FROM mail WHERE to_id=? AND acked_at=''`, q.ID).Scan(&newest); err != nil || newest <= q.WokeMail {
		return
	}
	if _, err := e.db.Exec(`UPDATE participants SET kind=?, was_session=1, woke_mail=?, status=?, turn='', turn_blocks=0 WHERE id=?`,
		KindWorker, newest, StatusStopped, q.ID); err != nil {
		return
	}
	q.Kind, q.WasSession, q.WokeMail, q.Status = KindWorker, true, newest, StatusStopped
	// Mail a turn of the session was given but did not finish is given again.
	e.regive(q.ID)
	if err := e.startWorker(q, true); err != nil {
		e.event("deny", t.Name, "could not wake %s for its mail: %v", q.Name, err)
		_, _ = e.db.Exec(`UPDATE participants SET kind=?, status=? WHERE id=?`, KindSession, StatusGone, q.ID)
		return
	}
	e.event("resume", t.Name, "%s woken by its mail as a worker", q.Name)
}

// takeBack makes w, a session woken as a worker, a session again: its person opened it. The worker
// is stopped; its mail not acked comes again with the session's turns.
func (e *Engine) takeBack(w *Participant) (Me, error) {
	e.stopRun(w.ID)
	delete(e.runs, w.ID) // its exit must not touch the session it is again
	if _, err := e.db.Exec(`UPDATE participants SET kind=?, was_session=0, status=?, last_active=? WHERE id=?`,
		KindSession, StatusLive, now(), w.ID); err != nil {
		return Me{}, err
	}
	e.regive(w.ID)
	e.event("resume", e.teamName(w.TeamID), "%s opened again: a session, no longer a worker", w.Name)
	w.Kind, w.WasSession, w.Status = KindSession, false, StatusLive
	return e.me(w), nil
}
