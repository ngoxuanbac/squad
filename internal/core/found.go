package core

import (
	"encoding/json"
	"fmt"

	"github.com/bacnx/squad/manifests"
)

// Founding a team: a solo session becomes the gate of a team made from a template.

// found makes the caller (a session) the gate of a new team from a template. A member of a team
// leaves it to do so (leave).
func (e *Engine) found(p *Participant, a AgentArgs) (string, error) {
	if p.Kind != KindSession {
		return "", errf("invalid", "a headless worker does not found a team")
	}
	m, err := manifests.Load(TemplatesDir(e.dir), firstNonEmpty(a.Template, "p2p"))
	if err != nil {
		return "", errf("not_found", "%v", err)
	}
	if old, _ := e.team(p.TeamID); old != nil {
		e.leave(p, old)
		e.event("left", old.Name, "%s left team %s", p.Name, old.Name)
	}
	// A team named like its founder is fine: the name reaches the founder either way (the gate).
	name, err := e.uniqueName(firstNonEmpty(a.Name, p.Name), p.ID)
	if err != nil {
		return "", err
	}
	t, err := e.newTeam(name, m, p.Cwd, p.ID, "")
	if err != nil {
		return "", err
	}
	role := m.SessionRole()
	if _, err := e.db.Exec(`UPDATE participants SET team_id=?, role=?, depth=0, spawned_by='' WHERE id=?`, t.ID, role, p.ID); err != nil {
		return "", err
	}
	p.TeamID, p.Role = t.ID, role
	e.event("found", t.Name, "%s founded team %s (%s) as %s", p.Name, t.Name, m.Template, role)
	return fmt.Sprintf("Team %s founded from template %s; you are its gate, role %s.\n"+
		"Wait for your workers' mail with the inbox tool (wait_seconds up to 600) instead of polling.\n\n%s",
		t.Name, m.Template, role, e.roleCard(t, p)), nil
}

func (e *Engine) newTeam(name string, m *manifests.Manifest, root, gate, parent string) (*Team, error) {
	mj, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	t := &Team{ID: newID("t_"), Name: name, Template: m.Template, Root: root, GateID: gate, ParentID: parent,
		Status: TeamOpen, CreatedAt: now(), M: m}
	_, err = e.db.Exec(`INSERT INTO teams(id,name,template,root,manifest,gate_id,parent_id,status,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Name, t.Template, t.Root, string(mj), t.GateID, t.ParentID, t.Status, t.CreatedAt)
	return t, err
}
