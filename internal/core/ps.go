package core

import (
	"fmt"
	"strings"

	"github.com/bacnx/squad/internal/view"
)

// View is everything squad runs (internal/view), for ps, top and the Paseo plugin.
func (e *Engine) View(pid int) (view.View, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v := view.View{Version: view.Version, Daemon: view.Daemon{Version: e.version, Pid: pid, StartedAt: e.started.Format("2006-01-02T15:04:05Z")},
		Teams: []view.Team{}, Solos: []view.Member{}, Events: []view.Event{}, Notices: e.notices(5)}
	open, err := e.tList(`status=?`, TeamOpen)
	if err != nil {
		return v, err
	}
	closed, err := e.tList(`status=? ORDER BY closed_at DESC LIMIT 20 --`, TeamClosed)
	if err != nil {
		return v, err
	}
	for _, t := range append(open, closed...) {
		vt := view.Team{ID: t.ID, Name: t.Name, Template: t.Template, Root: t.Root, Status: t.Status,
			Gate: e.nameOf(t.GateID), Parent: e.nameOf(t.ParentID), CreatedAt: t.CreatedAt, Members: []view.Member{}}
		ms, err := e.pList(`team_id=?`, t.ID)
		if err != nil {
			return v, err
		}
		for _, m := range ms {
			vt.Members = append(vt.Members, e.member(m))
			if t.Status == TeamOpen {
				e.count(&v.Summary, m)
			}
		}
		if t.Status == TeamOpen {
			v.Summary.Teams++
		}
		v.Teams = append(v.Teams, vt)
	}
	solos, err := e.pList(`team_id='' AND kind=? AND status!=?`, KindSession, StatusGone)
	if err != nil {
		return v, err
	}
	for _, s := range solos {
		v.Solos = append(v.Solos, e.member(s))
		e.count(&v.Summary, s)
	}
	rows, err := e.db.Query(`SELECT at,kind,team,text FROM (SELECT * FROM events ORDER BY id DESC LIMIT 50) ORDER BY id`)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var ev view.Event
		if err := rows.Scan(&ev.At, &ev.Kind, &ev.Team, &ev.Text); err != nil {
			return v, err
		}
		v.Events = append(v.Events, ev)
	}
	return v, rows.Err()
}

func (e *Engine) count(s *view.Summary, m *Participant) {
	if m.Kind == KindSession {
		if m.Status != StatusGone {
			s.Sessions++
		}
		return
	}
	if m.Status == StatusLive || m.Status == StatusWorking {
		s.Workers++
	}
	if m.Status == StatusWorking {
		s.Working++
	}
}

func (e *Engine) member(m *Participant) view.Member {
	n, _ := e.unreadFor(m)
	return view.Member{ID: m.ID, Name: m.Name, Role: m.Role, Kind: m.Kind, Status: m.Status, Model: m.Model,
		ReportsTo: e.nameOf(m.SpawnedBy), Task: m.Task, Unread: n, Cwd: m.Cwd, LastActive: m.LastActive}
}

// Who is the who tool: the caller's team, then one line per other team and solo session.
func (e *Engine) Who(token string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.byToken(token)
	if err != nil {
		return "", err
	}
	own, err := e.team(p.TeamID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if own == nil {
		b.WriteString(soloText(p.Name))
	} else {
		text, err := e.teamText(p, own)
		if err != nil {
			return "", err
		}
		b.WriteString(text)
	}
	teams, err := e.tList(`status=? AND id!=?`, TeamOpen, p.TeamID)
	if err != nil {
		return "", err
	}
	solos, err := e.pList(`team_id='' AND kind=? AND status!=? AND id!=?`, KindSession, StatusGone, p.ID)
	if err != nil {
		return "", err
	}
	if len(teams)+len(solos) > 0 {
		b.WriteString("\nOutside your team (reachable gate to gate: write to the team's name):\n")
		for _, t := range teams {
			extra := ""
			if t.ParentID != "" {
				extra = ", taskforce of " + e.nameOf(t.ParentID)
			}
			fmt.Fprintf(&b, "- team %s (%s%s), gate %s, root %s\n", t.Name, t.Template, extra, e.nameOf(t.GateID), t.Root)
		}
		for _, s := range solos {
			fmt.Fprintf(&b, "- solo %s, in %s\n", s.Name, s.Cwd)
		}
	}
	return b.String(), nil
}

// teamText lists the caller's team: who is in it and whom the caller may write to.
func (e *Engine) teamText(p *Participant, own *Team) (string, error) {
	if own == nil {
		return "You are in no team.\n", nil
	}
	ms, err := e.pList(`team_id=?`, own.ID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s, role %s, in team %s (template %s, %s), root %s.\n", p.Name, p.Role, own.Name, own.Template, own.Status, own.Root)
	if own.ParentID != "" {
		fmt.Fprintf(&b, "This team is a taskforce called by %s (write to it with to=\"caller\").\n", e.nameOf(own.ParentID))
	}
	b.WriteString("Members:\n")
	for _, m := range ms {
		var tags []string
		if m.ID == own.GateID {
			tags = append(tags, "gate")
		}
		if m.ID == p.ID {
			tags = append(tags, "you")
		} else if own.M.Allows(p.Role, m.Role) {
			tags = append(tags, "you may write")
		}
		if m.SpawnedBy != "" {
			tags = append(tags, "reports to "+e.nameOf(m.SpawnedBy))
		}
		fmt.Fprintf(&b, "- %s (%s, %s, %s)", m.Name, m.Role, m.Kind, m.Status)
		if len(tags) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(tags, "; "))
		}
		if m.Task != "" && m.ID != p.ID {
			fmt.Fprintf(&b, " task: %s", m.Task)
		}
		b.WriteString("\n")
	}
	tfs, _ := e.tList(`parent_id=? AND status=?`, p.ID, TeamOpen)
	for _, t := range tfs {
		fmt.Fprintf(&b, "Your taskforce: %s (%s), chair %s\n", t.Name, t.Template, e.nameOf(t.GateID))
	}
	return b.String(), nil
}
