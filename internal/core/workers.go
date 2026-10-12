package core

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bacnx/squad/internal/driver"
)

// startWorker starts q's process on its harness's Runtime (resume: its existing session) and
// hands it its mail.
// Callers hold e.mu.
func (e *Engine) startWorker(q *Participant, resume bool) error {
	t, err := e.team(q.TeamID)
	if err != nil {
		return err
	}
	if t == nil || t.Status != TeamOpen {
		return errf("closed", "%s's team is closed", q.Name)
	}
	if _, ok := e.runs[q.ID]; ok {
		return nil
	}
	if resume {
		if err := e.countRespawn(q, t); err != nil {
			return err
		}
	}
	if lim := int(t.M.Limits.Concurrency); lim > 0 {
		if n := e.liveWorkers(t.ID); n >= lim {
			return denyf("limit", "limits.concurrency", "%d live workers, limit %d (team %s)", n, lim, t.Name)
		}
	}
	rt, err := e.runtime(q.Harness)
	if err != nil {
		return err
	}
	var allow []string
	if r := t.M.Roles[q.Role]; r != nil {
		allow = r.Spawn.AllowTools
	}
	run, err := rt.Start(driver.Spec{
		ParticipantID: q.ID,
		Cwd:           q.Cwd,
		SessionRef:    q.SessionRef,
		Resume:        resume,
		Model:         q.Model,
		Thinking:      q.Thinking,
		RoleCard:      e.roleCard(t, q),
		Token:         q.Token,
		AllowTools:    allow,
		LogPath:       WorkerLog(e.dir, q.ID),
		RunDir:        RunDir(e.dir, q.ID),
	}, (*events)(e))
	if err != nil {
		return err
	}
	e.runs[q.ID] = run
	_, _ = e.db.Exec(`UPDATE participants SET status=? WHERE id=?`, StatusLive, q.ID)
	if resume {
		e.event("resume", t.Name, "%s resumed (pid %d)", q.Name, run.Pid())
	}
	e.deliver(q)
	return nil
}

// runtime is the Runtime of harness h ("" = the default one).
func (e *Engine) runtime(h string) (driver.Runtime, error) {
	if h == "" {
		h = e.defaultHarness
	}
	rt, ok := e.runtimes[h]
	if !ok {
		names := make([]string, 0, len(e.runtimes))
		for n := range e.runtimes {
			names = append(names, n)
		}
		slices.Sort(names)
		return nil, errf("invalid", "no runtime for harness %q (squad runs: %s)", h, orNone(names))
	}
	return rt, nil
}

// countRespawn refuses a worker that was resumed too often in the last hour (a crash loop).
func (e *Engine) countRespawn(q *Participant, t *Team) error {
	cut := time.Now().Add(-time.Hour)
	var keep []string
	for _, s := range strings.Fields(q.Respawns) {
		if ts, err := time.Parse(time.RFC3339, s); err == nil && ts.After(cut) {
			keep = append(keep, s)
		}
	}
	if lim := int(t.M.Limits.MaxRespawnPerHour); lim > 0 && len(keep) >= lim {
		reason := fmt.Sprintf("%s resumed %d times in the last hour; parked (limits.max_respawn_per_hour).", q.Name, len(keep))
		if q.Status != StatusParked {
			// Parked: not resumed by mail, still counted against concurrency; its lead is told.
			_, _ = e.db.Exec(`UPDATE participants SET status=? WHERE id=?`, StatusParked, q.ID)
			q.Status = StatusParked
			e.event("parked", t.Name, "%s", reason)
			if lead, _ := e.byID(q.SpawnedBy); lead != nil {
				e.engineMail(lead, "respawn_limit", reason)
			}
		}
		return denyf("limit", "limits.max_respawn_per_hour", "%s", reason)
	}
	keep = append(keep, now())
	_, err := e.db.Exec(`UPDATE participants SET respawns=? WHERE id=?`, strings.Join(keep, " "), q.ID)
	return err
}

func (e *Engine) liveWorkers(teamID string) int {
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM participants WHERE team_id=? AND kind=? AND status IN (?,?,?)`,
		teamID, KindWorker, StatusLive, StatusWorking, StatusParked).Scan(&n)
	return n
}

// stopRun stops q's process without waiting (the run's Exited callback takes e.mu).
func (e *Engine) stopRun(id string) {
	if r, ok := e.runs[id]; ok {
		go r.Stop(StopGrace)
	}
}

// roleCard is what a member is told about its place: appended to a worker's system prompt, and
// returned to a session that founds a team.
func (e *Engine) roleCard(t *Team, p *Participant) string {
	r := t.M.Roles[p.Role]
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s, the %s of squad team %q (template %s), rooted at %s.\n", p.Name, p.Role, t.Name, t.Template, t.Root)
	if r != nil && r.Description != "" {
		fmt.Fprintf(&b, "Your role: %s\n", r.Description)
	}
	if t.ParentID != "" && t.GateID == p.ID {
		fmt.Fprintf(&b, "This team is a taskforce called up by %s; send your answer to it (to=\"caller\").\n", e.nameOf(t.ParentID))
	} else if p.SpawnedBy != "" {
		fmt.Fprintf(&b, "You report to %s.\n", e.nameOf(p.SpawnedBy))
	}
	if targets := t.M.Targets(p.Role); len(targets) > 0 {
		fmt.Fprintf(&b, "You may write to members with role: %s (the who tool lists them by name).\n", strings.Join(targets, ", "))
	}
	if r != nil && len(r.CanSpawn) > 0 {
		fmt.Fprintf(&b, "You may spawn workers with role: %s (agent action=spawn role=... task=...).\n", strings.Join(r.CanSpawn, ", "))
	}
	if r != nil && len(r.CanCallTaskforce) > 0 {
		fmt.Fprintf(&b, "You may call up these taskforces: %s (agent action=spawn template=... task=...); the answer comes back to you as mail.\n", strings.Join(r.CanCallTaskforce, ", "))
	}
	if r != nil && r.Instructions != "" {
		b.WriteString("\n")
		b.WriteString(r.Instructions)
		b.WriteString("\n")
	}
	b.WriteString(e.sharedPrompts(t.Template, p.Role))
	return b.String()
}

// sharedPrompts is the text of the shared prompts (config.yaml) of a role, "" for none.
func (e *Engine) sharedPrompts(template, role string) string {
	if e.prompts == nil {
		return ""
	}
	return e.prompts(template, role)
}

// events receives a run's callbacks; each takes the engine's lock. A run no longer in e.runs (its
// session was taken back by its person) is not listened to.
type events Engine

func (v *events) Working(id string, working bool) {
	e := (*Engine)(v)
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, live := e.runs[id]; !live {
		return
	}
	status := StatusLive
	if working {
		status = StatusWorking
	} else {
		_, _ = e.db.Exec(`UPDATE participants SET last_turn_end=? WHERE id=?`, now(), id)
	}
	_, _ = e.db.Exec(`UPDATE participants SET status=?, last_active=? WHERE id=? AND status IN (?,?)`,
		status, now(), id, StatusLive, StatusWorking)
	if !working {
		// The turn ended: mail held back during it starts the next one.
		if e.waiters[id] != nil {
			return
		}
		if p, _ := e.byID(id); p != nil {
			e.deliver(p)
		}
	}
}

func (v *events) Completed(id string, batch int64, ok bool) {
	e := (*Engine)(v)
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, live := e.runs[id]; !live {
		return
	}
	_ = e.settle(id, batch, ok) // cancelled (an abort): given again with the next mail or resume
}

func (v *events) Exited(id string, err error) {
	e := (*Engine)(v)
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, live := e.runs[id]; !live {
		return
	}
	delete(e.runs, id)
	_, _ = e.db.Exec(`UPDATE participants SET status=? WHERE id=?`, StatusStopped, id)
	e.regive(id)
	p, _ := e.byID(id)
	if p == nil || e.closing {
		return
	}
	msg := "exited"
	if err != nil {
		msg = "exited: " + err.Error()
	}
	e.event("exit", e.teamName(p.TeamID), "%s %s", p.Name, msg)
}
