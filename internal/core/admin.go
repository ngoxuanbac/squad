package core

import (
	"fmt"
	"slices"
	"strings"

	"github.com/bacnx/squad/internal/driver"
)

// What the human does from the CLI (and the admin token): look, stop, resume, close.

func (e *Engine) target(name string) (*Participant, error) {
	p, err := e.pWhere(`name=? OR id=?`, name, name)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, errf("not_found", "no participant %q", name)
	}
	return p, nil
}

// Resolve is the participant a CLI argument names.
func (e *Engine) Resolve(name string) (*Participant, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.target(name)
}

// Kill stops a worker's process tree; new mail resumes it.
func (e *Engine) Kill(name string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.target(name)
	if err != nil {
		return "", err
	}
	if p.Kind != KindWorker {
		return "", errf("invalid", "%s is a session you opened: stop it in its own window", p.Name)
	}
	if _, ok := e.runs[p.ID]; !ok {
		return p.Name + " is not running", nil
	}
	e.stopRun(p.ID)
	e.event("stop", e.teamName(p.TeamID), "human stopped %s", p.Name)
	return "stopping " + p.Name, nil
}

// Interrupt cancels a worker's current turn; it stays alive.
func (e *Engine) Interrupt(name string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.target(name)
	if err != nil {
		return "", err
	}
	r, ok := e.runs[p.ID]
	if !ok {
		return "", errf("invalid", "%s is not a running worker", p.Name)
	}
	if err := r.Interrupt(); err != nil {
		return "", err
	}
	return "interrupted " + p.Name, nil
}

// Resume starts a stopped worker again in its Claude session; its unacked mail comes again.
func (e *Engine) Resume(name string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.target(name)
	if err != nil {
		return "", err
	}
	if p.Kind != KindWorker {
		return "", errf("invalid", "%s is a session, not a worker", p.Name)
	}
	return e.resume(p)
}

// TeamDown closes a team (or taskforce) by name.
func (e *Engine) TeamDown(name string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.tWhere(`name=? AND status=?`, name, TeamOpen)
	if err != nil {
		return "", err
	}
	if t == nil {
		return "", errf("not_found", "no open team %q", name)
	}
	e.closeTeam(t, "closed by the human")
	return "closed " + t.Name, nil
}

// SetModel sets a worker's model, thinking level or both ("" keeps it). A running worker switches
// first, from its next turn with its context kept; if its harness refuses, nothing is stored. The
// stored values are what its next start uses. A session's model is chosen in the session.
func (e *Engine) SetModel(name, model, thinking string) (string, error) {
	if model == "" && thinking == "" {
		return "", errf("invalid", "a model or a thinking level is required")
	}
	e.mu.Lock()
	p, err := e.target(name)
	if err == nil && p.Kind != KindWorker {
		err = errf("invalid", "%s is not a worker; its model is chosen in its own session", p.Name)
	}
	if err == nil && thinking != "" {
		// Checked here: a harness may take any level live and fail on it at the next start.
		var rt driver.Runtime
		if rt, err = e.runtime(p.Harness); err == nil && rt.Levels() != nil && !slices.Contains(rt.Levels(), thinking) {
			err = errf("invalid", "%s runs thinking %s, not %q", rt.Harness(), strings.Join(rt.Levels(), "|"), thinking)
		}
	}
	if err != nil {
		e.mu.Unlock()
		return "", err
	}
	r := e.runs[p.ID]
	e.mu.Unlock()
	// The answer comes on the run's stdout, whose reader may wait for e.mu: not held here.
	when := "from its next start"
	if r != nil {
		if err := r.Configure(model, thinking); err != nil {
			return "", errf("invalid", "%s: nothing stored: %v", p.Name, err)
		}
		when = "from its next turn"
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := e.db.Exec(`UPDATE participants SET model=COALESCE(NULLIF(?,''),model), thinking=COALESCE(NULLIF(?,''),thinking) WHERE id=?`,
		model, thinking, p.ID); err != nil {
		return "", err
	}
	q, _ := e.byID(p.ID)
	if q == nil {
		return "", errf("not_found", "%s is gone", p.Name)
	}
	e.event("model", e.teamName(q.TeamID), "%s: model %s, thinking %s, %s", q.Name, orDefault(q.Model), orDefault(q.Thinking), when)
	return fmt.Sprintf("%s: model %s, thinking %s, %s", q.Name, orDefault(q.Model), orDefault(q.Thinking), when), nil
}

func orDefault(s string) string {
	if s == "" {
		return "(harness default)"
	}
	return s
}

// Release delivers a held message (the admin's `squad release #N`): its hold and its cc copies'
// are cleared, and its recipients get it.
func (e *Engine) Release(ref string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	id, err := e.ref(ref)
	if err != nil {
		return "", err
	}
	if id == 0 {
		return "", errf("invalid", "release needs #N")
	}
	var held, teamID string
	if err := e.db.QueryRow(`SELECT held_reason, team_id FROM mail WHERE id=?`, id).Scan(&held, &teamID); err != nil {
		return "", err
	}
	if held == "" {
		return "", errf("invalid", "message #%d is not held", id)
	}
	if t, _ := e.team(teamID); t != nil && t.Status != TeamOpen {
		return "", denyf("invalid", "team.closed", "message #%d belongs to a closed team", id)
	}
	if _, err := e.db.Exec(`UPDATE mail SET held_reason='' WHERE id=? OR cc_of=?`, id, id); err != nil {
		return "", err
	}
	qs, err := e.pList(`id IN (SELECT to_id FROM mail WHERE id=? OR cc_of=?)`, id, id)
	if err != nil {
		return "", err
	}
	for _, q := range qs {
		e.handOver(q)
	}
	e.event("released", e.teamName(teamID), "#%d released to %d recipient(s)", id, len(qs))
	return fmt.Sprintf("released #%d", id), nil
}
