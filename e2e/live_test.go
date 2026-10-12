package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bacnx/squad/internal/driver/local"
)

// Live tests: the workers are the real claude (on PATH, logged in), the gate is still a scripted
// session. They cost money and need the network, so they run only with SQUAD_LIVE=1 (all of them
// but the dual-lens review, which runs three models) or SQUAD_LIVE=all. Each worker runs on haiku
// unless its role names a model, under --max-budget-usd 1; each test logs what its workers spent.

func newLiveEnv(t *testing.T, need string) *env {
	t.Helper()
	if live := os.Getenv("SQUAD_LIVE"); live != "all" && live != need {
		t.Skipf("live test: SQUAD_LIVE=%s", need)
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("no claude on PATH")
	}
	old := patience
	patience = 5 * time.Minute
	t.Cleanup(func() { patience = old })
	e := newEnv(t)
	b, _ := json.Marshal(map[string]any{"cmd": claude, "model": "haiku", "args": []string{"--max-budget-usd", "1"}})
	must(t, os.WriteFile(local.ClaudeProfilePath(e.home), b, 0o600)) // read at each spawn
	t.Cleanup(func() { t.Logf("workers spent $%.4f", e.spent()) })
	return e
}

// spent is what the workers' turns cost, from their logs, once the turns going on have ended (a
// minute at most: a turn's cost is logged at its end).
func (e *env) spent() float64 {
	for end := time.Now().Add(time.Minute); time.Now().Before(end); time.Sleep(time.Second) {
		working := false
		for _, t := range e.psQuiet().Teams {
			for _, m := range t.Members {
				working = working || m.Status == "working"
			}
		}
		if !working {
			break
		}
	}
	var usd float64
	for _, name := range e.workers() {
		out, _ := e.run("tail", name, "-n", "100000", "--json", "--no-start")
		for _, line := range strings.Split(out, "\n") {
			var r struct {
				Type string
				Cost float64 `json:"total_cost_usd"`
			}
			if json.Unmarshal([]byte(line), &r) == nil && r.Type == "result" {
				usd += r.Cost
			}
		}
	}
	return usd
}

// buggy writes a Go module whose test fails: Add subtracts.
func buggy(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range map[string]string{
		"go.mod":       "module calc\n\ngo 1.22\n",
		"calc.go":      "package calc\n\n// Add returns the sum of a and b.\nfunc Add(a, b int) int { return a - b }\n",
		"calc_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(2, 3); got != 5 {\n\t\tt.Fatalf(\"Add(2, 3) = %d, want 5\", got)\n\t}\n}\n",
	} {
		must(t, os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600))
	}
	return dir
}

// A worker finds and fixes a bug, proves it with the tests, and hands back by mail; the mail it
// got is acked once its turn is done.
func TestLiveFixFailingTest(t *testing.T) {
	e := newLiveEnv(t, "1")
	root := buggy(t)
	lead := e.founder(root, "lead-peer")
	lead.ok(spawn("peer", "fixer", "In your directory `go test ./...` fails. Find the bug in calc.go and fix it; do not change "+
		"calc_test.go. Run `go test ./...` again to prove it passes. Then send "+lead.name+" a handback (kind=handback) "+
		"saying in one line what you changed."))
	got := lead.mail(`sender="fixer"`)
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the tests still fail after the handback:\n%s\nhandback:\n%s", out, got)
	}
	e.waitStatus("fixer", "idle")
	if m := e.member("fixer"); m.Unread != 0 {
		t.Fatalf("fixer's mail not acked: %+v", m)
	}
}

// An abort cancels the real turn; its mail comes again and the worker does it all.
func TestLiveAbortGivesTheTaskAgain(t *testing.T) {
	e := newLiveEnv(t, "1")
	lead := e.founder(t.TempDir(), "lead-peer")
	lead.ok(spawn("peer", "sleeper", "Run the shell command `sleep 20` with your Bash tool. When it has finished, send "+
		lead.name+" the message: slept"))
	waitFor(t, "sleeper's sleep", func() bool {
		return strings.Contains(e.squad("tail", "sleeper", "-n", "200", "--no-start"), "sleep 20")
	})
	e.squad("abort", "sleeper")
	lead.mail("slept")
	if tail := e.squad("tail", "sleeper", "--json", "-n", "100000", "--no-start"); !strings.Contains(tail, `redelivered=\"true\"`) {
		t.Fatal("the task did not come again after the abort")
	}
}

// A refusal tells a real model which rule refused it, and it can act on that.
func TestLiveRefusalIsActionable(t *testing.T) {
	e := newLiveEnv(t, "1")
	lead := e.founder(t.TempDir(), "lead-peer")
	lead.ok(spawn("peer", "pinner", "Pin the note 'build is green' to your team's board (send with to=board). If squad refuses "+
		"it, send "+lead.name+" one message quoting the refusal's rule, the word in parentheses after 'refused'. Do nothing else."))
	if got := lead.mail(`sender="pinner"`); !strings.Contains(got, "can_pin") {
		t.Fatalf("pinner did not pass the refusal on:\n%s", got)
	}
}

// squad model switches a running real worker; a level Claude does not run is refused.
func TestLiveModelSwitch(t *testing.T) {
	e := newLiveEnv(t, "1")
	lead := e.founder(t.TempDir(), "lead-peer")
	lead.ok(spawn("peer", "w", "Reply to "+lead.name+" with the single word: ready"))
	lead.mail("ready")
	e.waitStatus("w", "idle")
	if out := e.squad("model", "w", "haiku", "--thinking", "low"); !strings.Contains(out, "low") {
		t.Fatalf("model: %s", out)
	}
	if out, err := e.run("model", "w", "--thinking", "extreme"); err == nil {
		t.Fatalf("a level Claude does not run was taken: %s", out)
	}
	lead.ok(send("w", "Reply to "+lead.name+" with the single word: again"))
	lead.mail("again")
}

// A dual-lens taskforce (a chair on haiku, lenses on sonnet and opus) reviews a diff with a bug
// and answers its caller.
func TestLiveDualLensReview(t *testing.T) {
	e := newLiveEnv(t, "dual-lens")
	root := buggy(t)
	caller := e.session(root)
	caller.ok(do("agent", map[string]any{"action": "spawn", "template": "dual-lens", "name": "review",
		"task": "Review calc.go in your directory: is Add correct?"}))
	got := caller.mail(`sender="review-chair"`)
	if !strings.Contains(got, "a + b") && !strings.Contains(strings.ToLower(got), "subtract") {
		t.Fatalf("the review missed the bug:\n%s", got)
	}
	// Both lenses were asked and answered (each ended a turn), on the models their roles name.
	for _, role := range []string{"lens-a", "lens-b"} {
		var lens string
		for _, tm := range e.ps().Teams {
			for _, m := range tm.Members {
				if m.Role == role {
					lens = m.Name
				}
			}
		}
		if lens == "" || !strings.Contains(e.squad("tail", lens, "-n", "100000", "--json", "--no-start"), `"type":"result"`) {
			t.Errorf("%s (%q) did not work on the review", role, lens)
		}
	}
}
