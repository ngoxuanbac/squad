package e2e

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bacnx/squad/internal/driver/local"
)

// A worker is a process tree (the fake claude and the squad MCP server it started): stopping the
// worker, or the daemon dying, must leave none of it running.

// procs are the pids the workers' processes marked (fakeclaude's mark).
func (e *env) procs() []int {
	e.t.Helper()
	files, err := filepath.Glob(filepath.Join(e.state, "pid-*"))
	must(e.t, err)
	var pids []int
	for _, f := range files {
		pid, err := strconv.Atoi(filepath.Base(f)[strings.LastIndexByte(filepath.Base(f), '-')+1:])
		must(e.t, err)
		pids = append(pids, pid)
	}
	return pids
}

// treeOfP1 runs p1 until its tree is up (its MCP server, and a process of a tool's that runs until
// killed) and returns it.
func treeOfP1(t *testing.T, e *env) []int {
	t.Helper()
	leadWithPeer(t, e, "@child").mail("done")
	pids := e.procs()
	if len(pids) != 3 {
		t.Fatalf("p1's processes: %v, want itself, its MCP server and its child", pids)
	}
	return pids
}

// gone waits until none of pids runs and says how long that took.
func gone(t *testing.T, pids []int) time.Duration {
	t.Helper()
	start := time.Now()
	waitFor(t, "the processes to end", func() bool {
		for _, p := range pids {
			if local.Alive(p) {
				return false
			}
		}
		return true
	})
	return time.Since(start)
}

// Killed or gone with the daemon, a worker's tree ends; one that hangs when its stdin closes is
// killed within the grace squad gives it.
func TestProcessTreeEnds(t *testing.T) {
	for _, c := range []struct {
		name        string
		hang, crash bool
	}{{"kill", false, false}, {"kill hung", true, false}, {"daemon crash", false, true}, {"daemon crash hung", true, true}} {
		t.Run(c.name, func(t *testing.T) {
			var vars []string
			if c.hang {
				vars = append(vars, "FAKE_HANG=1")
			}
			e := newEnv(t, vars...)
			pids := treeOfP1(t, e)
			if c.crash {
				e.crash()
			} else {
				e.squad("kill", "p1")
			}
			if d := gone(t, pids); d > 8*time.Second {
				t.Fatalf("took %s", d)
			}
		})
	}
}
