package core

// Closing teams and stopping workers.

// closeFor closes the caller's own team (the gate) or a taskforce it called.
func (e *Engine) closeFor(p *Participant, own *Team, name string) (string, error) {
	if name != "" && (own == nil || name != own.Name) {
		t, err := e.tWhere(`name=? AND status=?`, name, TeamOpen)
		if err != nil {
			return "", err
		}
		if t == nil || t.ParentID != p.ID {
			return "", denyf("permission", "taskforce.not_yours", "%q is not a taskforce you called up (only its caller closes it, from outside)", name)
		}
		e.closeTeam(t, "closed by its caller "+p.Name)
		return "closed taskforce " + t.Name, nil
	}
	if own == nil {
		return "", errf("invalid", "you are solo: there is no team to close")
	}
	if own.ParentID != "" {
		return "", denyf("permission", "taskforce.close_by_caller", "only %s closes this taskforce", e.nameOf(own.ParentID))
	}
	if p.Kind == KindWorker {
		return "", denyf("permission", "team_gate.close_headless", "a headless worker does not close its team")
	}
	if own.GateID != p.ID {
		return "", denyf("permission", "team_gate.close_not_gate", "only your team's gate %s closes the team; ask %s", firstNonEmpty(e.nameOf(own.GateID), "(none)"), firstNonEmpty(e.nameOf(own.GateID), "(none)"))
	}
	e.closeTeam(own, "closed by its gate "+p.Name)
	return "closed team " + own.Name + "; you are solo again", nil
}

// closeTeam stops a team's workers, makes its sessions solo again and closes the taskforces any
// of its members called.
func (e *Engine) closeTeam(t *Team, why string) {
	members, _ := e.pList(`team_id=?`, t.ID)
	_, _ = e.db.Exec(`UPDATE teams SET status=?, closed_at=? WHERE id=?`, TeamClosed, now(), t.ID)
	for _, m := range members {
		e.closeTaskforcesOf(m.ID, "its caller's team closed")
		if m.Kind == KindWorker {
			e.stopRun(m.ID)
			_, _ = e.db.Exec(`UPDATE participants SET status=? WHERE id=?`, StatusStopped, m.ID)
		} else {
			_, _ = e.db.Exec(`UPDATE participants SET team_id='', role='' WHERE id=?`, m.ID)
		}
	}
	e.event("close", t.Name, "team %s %s", t.Name, why)
}

func (e *Engine) closeTaskforcesOf(callerID, why string) {
	tfs, _ := e.tList(`parent_id=? AND status=?`, callerID, TeamOpen)
	for _, t := range tfs {
		e.closeTeam(t, why)
	}
}

// stopFor stops a worker the caller spawned (or any worker of the caller's team, for the gate).
func (e *Engine) stopFor(p *Participant, own *Team, name string) (string, error) {
	q, err := e.ownWorker(p, own, name)
	if err != nil {
		return "", err
	}
	e.stopRun(q.ID)
	e.event("stop", e.teamName(q.TeamID), "%s stopped %s", p.Name, q.Name)
	return "stopping " + q.Name + " (new mail to it resumes it)", nil
}
