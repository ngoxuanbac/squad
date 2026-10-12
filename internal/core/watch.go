package core

import (
	"fmt"
	"time"
)

// Tick is the daemon's periodic work: sessions without a heartbeat are gone, the watch rules of
// every open team are checked, and every open taskforce whose caller or chair is gone is closed, or
// whose members have all been idle long enough has its caller told.
func (e *Engine) Tick() {
	e.Sweep()
	e.mu.Lock()
	defer e.mu.Unlock()
	teams, _ := e.tList(`status=?`, TeamOpen)
	for _, t := range teams {
		e.watch(t)
		if t.ParentID != "" {
			e.taskforceTick(t)
		}
	}
}

// watch fires t's timers: a member of the rule's role working for longer than silent_for since it
// started working or its last turn ended is reported once per working stretch to the rule's
// target: its reports_to, or every other member of a role.
func (e *Engine) watch(t *Team) {
	for i, rule := range t.M.Timers {
		subjects, _ := e.pList(`team_id=? AND role=? AND status=?`, t.ID, rule.On, StatusWorking)
		for _, s := range subjects {
			from := max(s.StateSince, s.LastTurnEnd) // the stretch starts at the later of the two
			start, err := time.Parse(time.RFC3339, from)
			if err != nil || time.Since(start) <= rule.Silence() {
				continue
			}
			key := fmt.Sprintf("%s/timers[%d]/%s/%s", t.ID, i, s.ID, s.StateSince)
			res, err := e.db.Exec(`INSERT OR IGNORE INTO fired(key, at) VALUES(?,?)`, key, now())
			if err != nil {
				continue
			}
			if n, _ := res.RowsAffected(); n == 0 {
				continue // told already in this working stretch
			}
			var targets []*Participant
			if rule.Notify == "reports_to" {
				if lead, _ := e.byID(s.SpawnedBy); lead != nil {
					targets = append(targets, lead)
				}
			} else {
				targets, _ = e.pList(`team_id=? AND role=? AND id!=?`, t.ID, rule.Notify, s.ID)
			}
			for _, tg := range targets {
				_, label := e.label(tg, s.ID)
				e.engineMail(tg, "watch", fmt.Sprintf("%s has been working for %s with no turn end (rule silent_for %s).",
					label, time.Since(start).Round(time.Second), rule.SilentFor))
			}
			e.event("watch_fired", t.Name, "%s: working since %s, %d told", s.Name, from, len(targets))
		}
	}
}

// taskforceTick closes taskforce t when its caller is gone or its caller's team closed, or its
// chair stopped (the caller is told); else it tells the caller once per idle stretch that it may
// close it.
func (e *Engine) taskforceTick(t *Team) {
	caller, _ := e.byID(t.ParentID)
	if caller == nil || caller.Status == StatusGone {
		e.closeTeam(t, "closed: its caller is gone")
		return
	}
	if ct, _ := e.team(caller.TeamID); caller.TeamID != "" && (ct == nil || ct.Status != TeamOpen) {
		e.closeTeam(t, "closed: its caller's team closed")
		return
	}
	if chair, _ := e.byID(t.GateID); chair != nil && (chair.Status == StatusStopped || chair.Status == StatusParked) {
		e.closeTeam(t, "closed: its chair "+chair.Name+" stopped")
		e.engineMail(caller, "taskforce", fmt.Sprintf("Taskforce %s closed: its chair %s stopped.", t.Name, chair.Name))
		return
	}
	if t.M.Taskforce == nil || t.M.Taskforce.IdleFor == "" {
		return
	}
	idleFor, _ := time.ParseDuration(t.M.Taskforce.IdleFor)
	var busy int
	var since, noticed string
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM participants WHERE team_id=? AND status NOT IN (?,?,?)`,
		t.ID, StatusLive, StatusGone, StatusStopped).Scan(&busy)
	_ = e.db.QueryRow(`SELECT COALESCE(MAX(state_since), '') FROM participants WHERE team_id=?`, t.ID).Scan(&since)
	_ = e.db.QueryRow(`SELECT idle_noticed FROM teams WHERE id=?`, t.ID).Scan(&noticed)
	start, err := time.Parse(time.RFC3339, since)
	if busy > 0 || err != nil || time.Since(start) < idleFor || noticed == since {
		return
	}
	_, _ = e.db.Exec(`UPDATE teams SET idle_noticed=? WHERE id=?`, since, t.ID)
	e.engineMail(caller, "taskforce", fmt.Sprintf("Taskforce %s (template %s) has been idle for %s. If you do not need it any more, close it: agent action=close team=%s.",
		t.Name, t.Template, time.Since(start).Round(time.Second), t.Name))
}
