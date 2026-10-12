package core

import (
	"fmt"
	"slices"
	"strings"

	"github.com/bacnx/squad/manifests"
)

// taskforce calls up a temporary team from a template with a taskforce block. It works in the
// caller's directory (or cwd), its chair is a worker that is its gate, and it answers the caller
// by mail. A solo or a gate calls one, and so does a role whose template lists it in
// can_call_taskforce.
func (e *Engine) taskforce(p *Participant, own *Team, a AgentArgs) (string, error) {
	if a.Role != "" {
		return "", errf("invalid", "spawn takes role (a worker) or template (a taskforce), not both")
	}
	if strings.TrimSpace(a.Task) == "" {
		return "", errf("invalid", "spawn template= needs task: the question or job for the taskforce")
	}
	if own != nil {
		if own.ParentID != "" {
			return "", errf("permission", "a taskforce does not call another taskforce; ask your caller")
		}
		r := own.M.Roles[p.Role]
		if own.GateID != p.ID && (r == nil || !slices.Contains(r.CanCallTaskforce, a.Template)) {
			e.event("deny", own.Name, "%s may not call taskforce %s", p.Name, a.Template)
			return "", errf("permission", "only a solo, a team's gate, or a role with can_call_taskforce [%s] calls this taskforce; ask %s",
				a.Template, e.nameOf(own.GateID))
		}
	}
	m, err := manifests.Load(TemplatesDir(e.dir), a.Template)
	if err != nil {
		return "", errf("not_found", "%v", err)
	}
	if m.Taskforce == nil {
		return "", errf("invalid", "template %s has no taskforce block, so it cannot be called with spawn template= (found it instead)", a.Template)
	}
	dir := p.Cwd
	if a.Cwd != "" {
		root := p.Cwd
		if own != nil {
			root = own.Root
		}
		if dir, err = e.bounded(root, p.Cwd, a.Cwd); err != nil {
			return "", err
		}
	}
	name, err := e.uniqueName(firstNonEmpty(a.Name, a.Template), "")
	if err != nil {
		return "", err
	}
	t, err := e.newTeam(name, m, dir, "", p.ID)
	if err != nil {
		return "", err
	}
	chair := m.SessionRole()
	out, err := e.newWorker(p, t, chair, name+"-"+chair, dir, a.Model, a.Task, 1, true)
	if err != nil {
		_, _ = e.db.Exec(`DELETE FROM teams WHERE id=?`, t.ID)
		return "", err
	}
	e.event("found", t.Name, "%s called taskforce %s (%s)", p.Name, t.Name, m.Template)
	return fmt.Sprintf("taskforce %s (%s) called up: %s\nIts answer comes to you as mail. Close it when done: agent action=close team=%s",
		t.Name, m.Template, out, t.Name), nil
}
