package core

import (
	"fmt"
	"slices"
	"strings"

	"github.com/bacnx/squad/internal/view"
)

// Team lifecycle beyond found and close: a member leaving (it founds another team), the gate
// passing on, admitting a solo session, reopening a closed team, and an agent resuming or reading
// the log of a worker of its team.

// leave takes p out of team t (it founds another team). Its mail from t's members goes to t's gate,
// the members that reported to it report to the gate, the taskforces it called close, and when it
// was the gate the next member becomes the gate. A team left with a gate that is not live, or none,
// gets a gate_lost notice. Callers hold e.mu.
func (e *Engine) leave(p *Participant, t *Team) {
	if t.GateID == p.ID {
		next := e.nextGate(t, p.ID)
		_, _ = e.db.Exec(`UPDATE teams SET gate_id=? WHERE id=?`, next, t.ID)
		e.event("gate_moved", t.Name, "the gate of %s passes from %s to %s", t.Name, p.Name, firstNonEmpty(e.nameOf(next), "(none)"))
		t.GateID = next
	}
	e.closeTaskforcesOf(p.ID, "its caller "+p.Name+" left its team")
	gate, _ := e.byID(t.GateID)
	moved, workers := 0, 0
	if gate != nil {
		res, _ := e.db.Exec(`UPDATE mail SET to_id=? WHERE to_id=? AND team_id=? AND acked_at='' AND from_id!=?`, gate.ID, p.ID, t.ID, p.ID)
		n, _ := res.RowsAffected()
		moved = int(n)
		res, _ = e.db.Exec(`UPDATE participants SET spawned_by=? WHERE spawned_by=? AND team_id=? AND id!=?`, gate.ID, p.ID, t.ID, gate.ID)
		n, _ = res.RowsAffected()
		workers = int(n)
		e.event("rerouted", t.Name, "%d message(s) and %d worker(s) of %s go to the gate %s", moved, workers, p.Name, gate.Name)
	} else {
		var n int
		_ = e.db.QueryRow(`SELECT COUNT(*) FROM participants WHERE spawned_by=? AND team_id=?`, p.ID, t.ID).Scan(&n)
		workers = n
	}
	switch {
	case gate == nil:
		e.notice("gate_lost", t, "", fmt.Sprintf("%s left team %s, which has no live member now: %d workers of %s wait for the team's next gate.",
			p.Name, t.Name, workers, p.Name))
	case gate.Status == StatusGone:
		e.notice("gate_lost", t, "", fmt.Sprintf("%s left team %s; its gate %s is not live: %d unacked messages and %d workers of %s wait for %s.",
			p.Name, t.Name, gate.Name, moved, workers, p.Name, gate.Name))
	default:
		e.handOver(gate)
	}
}

// nextGate is the member of t (other than but) that takes the gate: sessions before workers, then
// in join order, the first whose role has send; "" when none.
func (e *Engine) nextGate(t *Team, but string) string {
	ms, _ := e.pList(`team_id=? AND id!=? ORDER BY kind!=?, created_at, name --`, t.ID, but, KindSession)
	for _, m := range ms {
		if t.M.HasTool(m.Role, "send") {
			return m.ID
		}
	}
	return ""
}

// admit puts a solo session at the team's root into p's team with role a.Role. Any member whose
// role may spawn that role may admit; the team's gate is not needed.
func (e *Engine) admit(p *Participant, own *Team, a AgentArgs) (string, error) {
	if a.Name == "" || a.Role == "" {
		return "", errf("invalid", "admit needs name (the solo session) and role")
	}
	if p.Kind == KindWorker {
		return "", denyf("permission", "admit.headless", "a headless worker does not admit")
	}
	if own == nil {
		return "", errf("invalid", "you are in no team")
	}
	if _, ok := own.M.Roles[a.Role]; !ok {
		return "", errf("invalid", "role %q not in team manifest", a.Role)
	}
	if r := own.M.Roles[p.Role]; r == nil || !slices.Contains(r.CanSpawn, a.Role) {
		return "", denyf("permission", "can_spawn", "role %s cannot admit into %s", p.Role, a.Role)
	}
	q, err := e.pWhere(`(name=? OR id=?) AND team_id='' AND kind=? AND status!=?`, a.Name, a.Name, KindSession, StatusGone)
	if err != nil {
		return "", err
	}
	if q == nil {
		return "", denyf("not_found", "admit.not_solo", "%s is not a live solo session", a.Name)
	}
	if !samePath(q.Cwd, own.Root) {
		return "", denyf("bounds", "admit.not_at_root", "%s is in %s, not at the team root %s", q.Name, q.Cwd, own.Root)
	}
	if _, err := e.db.Exec(`UPDATE participants SET team_id=?, role=?, spawned_by=?, depth=? WHERE id=?`,
		own.ID, a.Role, p.ID, p.Depth+1, q.ID); err != nil {
		return "", err
	}
	if own.GateID == "" && own.M.HasTool(a.Role, "send") {
		_, _ = e.db.Exec(`UPDATE teams SET gate_id=? WHERE id=?`, q.ID, own.ID)
	}
	e.event("admitted", own.Name, "%s admitted %s as %s", p.Name, q.Name, a.Role)
	return fmt.Sprintf("admitted %s as %s; it reports to you", q.Name, a.Role), nil
}

// reopen brings a closed team back with the caller, a solo session at its root, as its gate. Its
// workers stay stopped (resume them); mail waiting for its old gate goes to the new one.
func (e *Engine) reopen(p *Participant, a AgentArgs) (string, error) {
	if a.Team == "" {
		return "", errf("invalid", "reopen needs team (the closed team's name)")
	}
	if p.TeamID != "" || p.Kind != KindSession {
		return "", denyf("permission", "reopen.not_solo", "only a solo session reopens a team; you are in a team")
	}
	if open, _ := e.tWhere(`name=? AND status=?`, a.Team, TeamOpen); open != nil {
		return "", errf("invalid", "team %s is open: send to its gate or ask a member to admit you", a.Team)
	}
	t, err := e.tWhere(`name=? AND status=? ORDER BY (root=?) DESC, closed_at DESC LIMIT 1 --`, a.Team, TeamClosed, p.Cwd)
	if err != nil {
		return "", err
	}
	if t == nil {
		return "", errf("not_found", "no closed team %s", a.Team)
	}
	if t.ParentID != "" {
		return "", errf("invalid", "team %s was a taskforce: it is not reopened; call its template up again", t.Name)
	}
	if !samePath(t.Root, p.Cwd) {
		return "", denyf("bounds", "reopen.not_at_root", "team %s is rooted at %s; you are in %s", t.Name, t.Root, p.Cwd)
	}
	role := t.M.SessionRole()
	old := t.GateID
	if _, err := e.db.Exec(`UPDATE teams SET status=?, closed_at='', gate_id=? WHERE id=?`, TeamOpen, p.ID, t.ID); err != nil {
		return "", err
	}
	if _, err := e.db.Exec(`UPDATE participants SET team_id=?, role=?, depth=0 WHERE id=?`, t.ID, role, p.ID); err != nil {
		return "", err
	}
	moved := 0
	if old != "" && old != p.ID {
		res, _ := e.db.Exec(`UPDATE mail SET to_id=? WHERE to_id=? AND team_id=? AND acked_at=''`, p.ID, old, t.ID)
		n, _ := res.RowsAffected()
		moved = int(n)
		_, _ = e.db.Exec(`UPDATE participants SET spawned_by=? WHERE spawned_by=? AND team_id=?`, p.ID, old, t.ID)
	}
	p.TeamID, p.Role = t.ID, role
	t.Status, t.GateID = TeamOpen, p.ID
	e.event("found", t.Name, "%s reopened team %s as %s", p.Name, t.Name, role)
	if moved > 0 {
		e.handOver(p)
	}
	return e.reopenSummary(p, t), nil
}

// reopenSummary is what the reopener is told: the members, the board, its mail.
func (e *Engine) reopenSummary(p *Participant, t *Team) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Reopened team %s (template %s); you are %s (%s), its gate.\nMembers:", t.Name, t.Template, p.Name, p.Role)
	ms, _ := e.pList(`team_id=? AND id!=?`, t.ID, p.ID)
	if len(ms) == 0 {
		b.WriteString(" none besides you")
	}
	workers := false
	for _, m := range ms {
		n, _ := e.unread(m.ID)
		fmt.Fprintf(&b, "\n- %s (%s, %s): %s, last active %s", m.Name, m.Role, m.Kind, m.Status, m.LastActive)
		if n > 0 {
			fmt.Fprintf(&b, ", %d unacked message(s)", n)
		}
		workers = workers || m.Kind == KindWorker
	}
	pins, _ := e.board(p)
	fmt.Fprintf(&b, "\nBoard: %d pin(s)", len(pins))
	for _, pin := range pins {
		fmt.Fprintf(&b, "\n- #%d %s", pin.ID, title(pin.Body))
	}
	n, _ := e.unread(p.ID)
	fmt.Fprintf(&b, "\nUnacked mail to you: %d (read it with inbox).", n)
	if workers {
		b.WriteString("\nWorkers are stopped: agent action=tail name=<worker> shows where one was; action=resume brings it back with its context.")
	}
	return b.String()
}

// resumeFor is agent action=resume: the worker's spawner, or its team's gate, starts it again in
// its session.
func (e *Engine) resumeFor(p *Participant, own *Team, name string) (string, error) {
	q, err := e.ownWorker(p, own, name)
	if err != nil {
		return "", err
	}
	return e.resume(q)
}

// resume starts worker q again in its session; its unacked mail comes again.
func (e *Engine) resume(q *Participant) (string, error) {
	if t, _ := e.team(q.TeamID); t == nil || t.Status != TeamOpen {
		return "", errf("closed", "%s's team is closed", q.Name)
	}
	if _, ok := e.runs[q.ID]; ok {
		return q.Name + " is already running", nil
	}
	if err := e.startWorker(q, true); err != nil {
		return "", err
	}
	return "resumed " + q.Name, nil
}

// tailFor is agent action=tail: the last lines of a worker of the caller's.
func (e *Engine) tailFor(p *Participant, own *Team, name string) (string, error) {
	q, err := e.ownWorker(p, own, name)
	if err != nil {
		return "", err
	}
	lines, err := view.ReadTail(WorkerLog(e.dir, q.ID), 30)
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return q.Name + " has no log yet", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s), last %d lines:", q.Name, q.Status, len(lines))
	for _, l := range lines {
		fmt.Fprintf(&b, "\n%s: %s", l.Kind, l.Text)
		if l.Rest != "" {
			b.WriteString("  " + l.Rest)
		}
	}
	return b.String(), nil
}

// ownWorker is the worker name of the caller's team that the caller spawned, or any of the team's
// workers when the caller is its gate.
func (e *Engine) ownWorker(p *Participant, own *Team, name string) (*Participant, error) {
	q, err := e.pWhere(`(name=? OR id=?) AND kind=?`, name, name, KindWorker)
	if err != nil {
		return nil, err
	}
	if q == nil {
		return nil, errf("not_found", "no worker named %q", name)
	}
	if q.SpawnedBy != p.ID && (own == nil || own.GateID != p.ID || q.TeamID != own.ID) {
		return nil, denyf("permission", "worker.not_yours", "%s is not a worker you spawned", q.Name)
	}
	return q, nil
}

// samePath compares two directories as the OS would (case-insensitive on Windows).
func samePath(a, b string) bool { return cleanPath(a) == cleanPath(b) }
