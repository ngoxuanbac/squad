package e2e

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/bacnx/squad/manifests"
)

// The gate, from the templates themselves: for every built-in team template a team with two
// members of each role it can reach, each of which writes to every other member and tries to spawn
// every role it may not. What the daemon lets through must be what the template says, and what
// `squad why` says it would.
func TestGateFollowsTemplates(t *testing.T) {
	infos, err := manifests.List(t.TempDir())
	must(t, err)
	for _, info := range infos {
		if info.Taskforce {
			continue
		}
		t.Run(info.Name, func(t *testing.T) {
			m, err := manifests.Load(t.TempDir(), info.Name)
			must(t, err)
			gateMatrix(t, m)
		})
	}
}

// node is one member of the probe team.
type node struct {
	name, role string
	kids       []*node
	probes     []string // its sends to every other member, its spawns of every role it may not
}

func gateMatrix(t *testing.T, m *manifests.Manifest) {
	e := newEnv(t)
	lead := e.founder(t.TempDir(), m.Template)
	root := &node{name: lead.name, role: m.SessionRole()}
	all := []*node{root}
	// Breadth first: each role that can be spawned gets two members, from the first member of a
	// role that may spawn it.
	for i := 0; i < len(all); i++ {
		for _, r := range m.Roles[all[i].role].CanSpawn {
			if slices.ContainsFunc(all[1:], func(n *node) bool { return n.role == r }) {
				continue
			}
			for k := 1; k <= 2; k++ {
				kid := &node{name: fmt.Sprintf("%s%d", r, k), role: r}
				all[i].kids = append(all[i].kids, kid)
				all = append(all, kid)
			}
		}
	}
	for _, n := range all {
		for _, o := range all {
			if o != n {
				n.probes = append(n.probes, send(o.name, "probe"))
			}
		}
		for r := range m.Roles {
			if !slices.Contains(m.Roles[n.role].CanSpawn, r) {
				n.probes = append(n.probes, spawn(r, "no-"+n.name+"-"+r))
			}
		}
	}
	var spawns func(n *node) []string
	spawns = func(n *node) []string {
		var ds []string
		for _, k := range n.kids {
			task := append(append(spawns(k), "@await go"), k.probes...)
			ds = append(ds, spawn(k.role, k.name, task...))
		}
		return ds
	}

	// The founder spawns the tree; once every member is there, all of them probe at once.
	for _, c := range lead.turn(lines(spawns(root)...)) {
		if c.Err {
			t.Fatalf("%s: %s", c.Input, c.Text)
		}
	}
	waitFor(t, "every member", func() bool {
		v := e.ps()
		return len(v.Teams) == 1 && len(v.Teams[0].Members) == len(all)
	})
	e.release("go")
	calls := map[string][]call{root.name: lead.turn(lines(root.probes...))}
	waitFor(t, "every member's probes", func() bool {
		for _, n := range all[1:] {
			calls[n.name] = e.toolCalls(n.name)
			if c := calls[n.name]; len(c) < len(n.kids)+len(n.probes) || !c[len(c)-1].Done {
				return false
			}
		}
		return true
	})

	for _, n := range all {
		for _, c := range calls[n.name] {
			var in struct{ To, Role, Name string }
			_ = json.Unmarshal([]byte(c.Input), &in)
			switch {
			case c.Tool == "send":
				to := all[slices.IndexFunc(all, func(o *node) bool { return o.name == in.To })]
				want := m.HasTool(n.role, "send") && m.Allows(n.role, to.role)
				if c.Err == want {
					t.Errorf("%s (%s) -> %s (%s): allowed=%v, template says %v: %s", n.name, n.role, to.name, to.role, !c.Err, want, c.Text)
				}
				why := e.squad("why", n.name, to.name, "--no-start")
				if got := strings.Contains(why, "verdict: allow"); got != want {
					t.Errorf("squad why %s %s: allow=%v, template says %v:\n%s", n.name, to.name, got, want, why)
				}
			case c.Tool == "agent" && strings.HasPrefix(in.Name, "no-"):
				if !c.Err {
					t.Errorf("%s (%s) spawned a %s", n.name, n.role, in.Role)
				}
			case c.Tool == "agent" && c.Err:
				t.Errorf("%s (%s) could not spawn %s: %s", n.name, n.role, in.Name, c.Text)
			}
		}
	}
}
