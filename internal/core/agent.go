package core

import (
	"fmt"
	"slices"
	"strings"

	"github.com/bacnx/squad/internal/driver"
	"github.com/bacnx/squad/manifests"
)

// AgentArgs are the agent tool's arguments.
type AgentArgs struct {
	Action   string `json:"action"`   // templates | found | spawn | close | stop | team | admit | reopen | resume | tail
	Template string `json:"template"` // found; spawn: a taskforce
	Role     string `json:"role"`     // spawn: a worker of this role; admit: the role it takes
	Name     string `json:"name"`     // found: team name; spawn: worker or taskforce name; stop, resume, tail: worker; admit: the solo
	Task     string `json:"task"`     // spawn: the first mail
	Cwd      string `json:"cwd"`      // spawn: where it works
	Model    string `json:"model"`    // spawn: overrides the role's model
	Team     string `json:"team"`     // close: a taskforce you called; reopen: the closed team
}

// Agent is the agent tool.
func (e *Engine) Agent(token string, a AgentArgs) (string, error) {
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
	if own != nil && a.Action != "templates" && a.Action != "team" && !own.M.HasTool(p.Role, "agent") {
		return "", denyf("permission", "tools.not_granted", "role %s has no tool %q", p.Role, "agent")
	}
	switch a.Action {
	case "templates":
		list, err := manifests.List(TemplatesDir(e.dir))
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, t := range list {
			tf := ""
			if t.Taskforce {
				tf = " [taskforce: spawn template=" + t.Name + "]"
			}
			fmt.Fprintf(&b, "- %s (%s)%s: %s\n", t.Name, t.Origin, tf, t.Summary)
		}
		return b.String(), nil
	case "found":
		return e.found(p, a)
	case "spawn":
		if a.Template != "" {
			return e.taskforce(p, own, a)
		}
		return e.spawn(p, own, a)
	case "close":
		return e.closeFor(p, own, a.Team)
	case "stop":
		return e.stopFor(p, own, a.Name)
	case "team":
		return e.teamText(p, own)
	case "admit":
		return e.admit(p, own, a)
	case "reopen":
		return e.reopen(p, a)
	case "resume":
		return e.resumeFor(p, own, a.Name)
	case "tail":
		return e.tailFor(p, own, a.Name)
	default:
		return "", errf("invalid", "unknown action %q: templates, found, spawn, close, stop, team, admit, reopen, resume, tail", a.Action)
	}
}

// spawn starts a worker of a role the caller's role may spawn, and gives it its task.
func (e *Engine) spawn(p *Participant, own *Team, a AgentArgs) (string, error) {
	if own == nil {
		return "", errf("invalid", "you are in no team: found one first (agent action=found template=...), or call a taskforce (spawn template=...)")
	}
	if own.Status != TeamOpen {
		return "", errf("closed", "team %s is closed", own.Name)
	}
	r := own.M.Roles[p.Role]
	if r == nil || !slices.Contains(r.CanSpawn, a.Role) {
		var may []string
		if r != nil {
			may = r.CanSpawn
		}
		e.event("deny", own.Name, "%s may not spawn %q", p.Name, a.Role)
		return "", errf("permission", "role %s may not spawn role %q; it may spawn: %s", p.Role, a.Role, orNone(may))
	}
	if strings.TrimSpace(a.Task) == "" {
		return "", errf("invalid", "spawn needs task: the worker's first mail (its brief)")
	}
	if !own.M.Allows(p.Role, a.Role) {
		return "", denyf("routing", "routing.no_rule", "routing does not allow %s -> %s: a worker gets its task as mail from its spawner", p.Role, a.Role)
	}
	if lim := int(own.M.Limits.Depth); lim > 0 && p.Depth+1 > lim {
		return "", denyf("limit", "limits.depth", "depth %d exceeds %d (team %s)", p.Depth+1, lim, own.Name)
	}
	dir := p.Cwd
	if a.Cwd != "" {
		if !r.CanSetCwd {
			return "", errf("permission", "role %s may not set cwd", p.Role)
		}
		var err error
		if dir, err = e.bounded(own.Root, p.Cwd, a.Cwd); err != nil {
			return "", err
		}
	}
	return e.newWorker(p, own, a.Role, firstNonEmpty(a.Name, a.Role), dir, a.Model, a.Task, p.Depth+1, false)
}

func (e *Engine) newWorker(p *Participant, t *Team, role, name, dir, model, task string, depth int, chair bool) (string, error) {
	name, err := e.uniqueName(name, "")
	if err != nil {
		return "", err
	}
	rt, model, thinking, err := e.workerChain(p, t, role, model)
	if err != nil {
		return "", err
	}
	q := &Participant{ID: newID("p_"), Name: name, TeamID: t.ID, Role: role, Kind: KindWorker, Status: StatusStopped,
		Cwd: dir, SessionRef: rt.NewSessionRef(), SpawnedBy: p.ID, Model: model, Thinking: thinking,
		Token: newToken(), Depth: depth, CreatedAt: now(), LastActive: now(), Harness: rt.Harness()}
	if _, err := e.db.Exec(`INSERT INTO participants(id,name,team_id,role,kind,status,cwd,session_ref,spawned_by,model,thinking,token,depth,created_at,last_active,harness)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, q.ID, q.Name, q.TeamID, q.Role, q.Kind, q.Status, q.Cwd, q.SessionRef, q.SpawnedBy,
		q.Model, q.Thinking, q.Token, q.Depth, q.CreatedAt, q.LastActive, q.Harness); err != nil {
		return "", err
	}
	// The task is the first mail; startWorker hands it over.
	if _, err := e.insertMail(p.ID, q.ID, p.TeamID, task, "", "assign", 0, 0, "", ""); err != nil {
		return "", err
	}
	_, _ = e.db.Exec(`UPDATE participants SET task=? WHERE id=?`, title(task), q.ID)
	if chair { // a taskforce's chair is its gate before it starts, so its role card says so
		_, _ = e.db.Exec(`UPDATE teams SET gate_id=? WHERE id=?`, q.ID, t.ID)
	}
	if err := e.startWorker(q, false); err != nil {
		_, _ = e.db.Exec(`DELETE FROM mail WHERE to_id=?`, q.ID)
		_, _ = e.db.Exec(`DELETE FROM participants WHERE id=?`, q.ID)
		return "", err
	}
	e.event("spawn", t.Name, "%s spawned %s (%s)", p.Name, q.Name, role)
	return fmt.Sprintf("spawned %s (role %s) in %s; it reports to you. Its handback comes as mail.", q.Name, role, dir), nil
}

// workerChain picks a new worker's harness, model and thinking level, each from the first link
// that names one:
//   - harness: the role's spawn.harness, the founding session's (the gate's), the daemon default;
//   - model: the spawn's own model, the role's spawn.model, the harness profile;
//   - thinking: the role's spawn.thinking, the harness profile.
//
// "" or "inherit" names none; none at all leaves it to the harness (Claude's default model).
func (e *Engine) workerChain(p *Participant, t *Team, role, model string) (rt driver.Runtime, m, thinking string, err error) {
	rs := t.M.Roles[role].Spawn
	harness := named(rs.Harness)
	if harness == "" {
		if g, _ := e.byID(t.GateID); g != nil && g.Kind == KindSession {
			harness = g.Harness
		} else if p.Kind == KindSession {
			harness = p.Harness
		}
		if _, ok := e.runtimes[harness]; !ok {
			harness = "" // a session on a harness squad runs no workers on: the default
		}
	}
	if rt, err = e.runtime(harness); err != nil {
		return nil, "", "", err
	}
	pm, pt := rt.Defaults()
	return rt, firstNonEmpty(named(model), named(rs.Model), pm), firstNonEmpty(named(rs.Thinking), pt), nil
}

// named is v, or "" when it leaves the choice to the next link.
func named(v string) string {
	if v = strings.TrimSpace(v); v == "inherit" {
		return ""
	}
	return v
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return strings.TrimSpace(x)
		}
	}
	return ""
}
