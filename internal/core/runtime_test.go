package core

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bacnx/squad/internal/driver"
)

// memRuntime is a Runtime that starts no process: it records each Spec, and its runs complete
// every batch at once.
type memRuntime struct {
	name            string
	model, thinking string
	levels          []string

	mu    sync.Mutex
	specs []driver.Spec
}

func (m *memRuntime) Harness() string            { return m.name }
func (m *memRuntime) Defaults() (string, string) { return m.model, m.thinking }
func (m *memRuntime) NewSessionRef() string      { return driver.NewUUID() }
func (m *memRuntime) Levels() []string           { return m.levels }
func (m *memRuntime) last() driver.Spec {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.specs[len(m.specs)-1]
}
func (m *memRuntime) count() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.specs) }
func (m *memRuntime) Start(s driver.Spec, ev driver.Events) (driver.Run, error) {
	m.mu.Lock()
	m.specs = append(m.specs, s)
	m.mu.Unlock()
	return &memRun{id: s.ParticipantID, ev: ev, done: make(chan struct{})}, nil
}

type memRun struct {
	id   string
	ev   driver.Events
	once sync.Once
	done chan struct{}
}

func (r *memRun) Pid() int { return 4242 }
func (r *memRun) Deliver(batch int64, _ string) error {
	go r.ev.Completed(r.id, batch, true)
	return nil
}
func (r *memRun) Interrupt() error { return nil }
func (r *memRun) Configure(model, thinking string) error {
	if model == "bad" || thinking == "bad" {
		return errors.New("not available")
	}
	return nil
}
func (r *memRun) Stop(time.Duration) {
	r.once.Do(func() { close(r.done); r.ev.Exited(r.id, nil) })
}

func memEngine(t *testing.T, rts ...driver.Runtime) *Engine {
	t.Helper()
	e, err := Open(Options{Dir: t.TempDir(), Version: "test", Runtimes: rts})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}

// writeTemplate puts a template of the user's in e's home.
func writeTemplate(t *testing.T, e *Engine, name, yaml string) {
	t.Helper()
	dir := filepath.Join(TemplatesDir(e.dir), name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
}

const chainTemplate = `template: chain
auto_join_role: lead
roles:
  lead:
    tools: [send, inbox, who, agent]
    can_spawn: [plain, pinned, other]
  plain:
    tools: [send]
  pinned:
    tools: [send]
    spawn: {model: opus, thinking: high, allow_tools: [Monitor]}
  other:
    tools: [send]
    spawn: {harness: codex}
routing:
  - {from: lead, to: plain}
  - {from: lead, to: pinned}
  - {from: lead, to: other}
limits: {depth: 2, concurrency: 10}
`

func TestWorkerChain(t *testing.T) {
	claude := &memRuntime{name: "claude", model: "sonnet", thinking: "medium"}
	codex := &memRuntime{name: "codex"}
	e := memEngine(t, claude, codex)
	writeTemplate(t, e, "chain", chainTemplate)
	lead := must(e.RegisterSession(t.TempDir(), "", "k1", "claude"))
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "chain"}))

	// Nothing named: the founding session's harness, the profile's model and thinking.
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "plain", Task: "t"}))
	if s := claude.last(); s.Model != "sonnet" || s.Thinking != "medium" || len(s.AllowTools) != 0 {
		t.Fatalf("plain: %+v", s)
	}
	// The spawn's model wins over the profile.
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "plain", Task: "t", Model: "haiku"}))
	if s := claude.last(); s.Model != "haiku" {
		t.Fatalf("spawn model: %+v", s)
	}
	// The role's spawn block over the profile, and its allow_tools reach the runtime.
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "pinned", Task: "t"}))
	if s := claude.last(); s.Model != "opus" || s.Thinking != "high" || !slices.Equal(s.AllowTools, []string{"Monitor"}) {
		t.Fatalf("pinned: %+v", s)
	}
	// The role's harness over the session's.
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "other", Task: "t"}))
	if codex.count() != 1 {
		t.Fatal("role harness codex not used")
	}
	e.mu.Lock()
	p, _ := e.pWhere(`role=?`, "other")
	e.mu.Unlock()
	if p.Harness != "codex" {
		t.Fatalf("worker harness %q", p.Harness)
	}
	// Each run gets the worker's token, log and run directory, and its role card.
	s := claude.last()
	if s.Token == "" || !strings.HasSuffix(s.LogPath, ".jsonl") || s.RunDir == "" || !strings.Contains(s.RoleCard, "pinned") {
		t.Fatalf("spec: %+v", s)
	}
}

func TestWorkerChainFallbacks(t *testing.T) {
	claude := &memRuntime{name: "claude"}
	e := memEngine(t, claude)
	writeTemplate(t, e, "chain", chainTemplate)
	// A session on a harness squad runs no workers on: the default runtime.
	lead := must(e.RegisterSession(t.TempDir(), "", "k", "gemini"))
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "chain"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "plain", Task: "t"}))
	if claude.count() != 1 {
		t.Fatal("default runtime not used")
	}
	// A role naming a harness squad has no runtime for: refused, nothing started.
	_, err := e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "other", Task: "t"})
	refused(t, err, "invalid")
	if !strings.Contains(err.Error(), "codex") || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("refusal says %v", err)
	}
	if claude.count() != 1 {
		t.Fatal("something started for the refused spawn")
	}
}

// A worker resumed by mail runs again on the same harness, in the same session.
func TestResumeKeepsHarnessAndSession(t *testing.T) {
	claude := &memRuntime{name: "claude"}
	e := memEngine(t, claude)
	lead := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "lead-peer"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "p1", Task: "t"}))
	first := claude.last()
	must(e.Kill("p1"))
	eventually(t, e, "p1 stopped", func() bool { return e.runs[mustID(e, "p1")] == nil })
	must(e.Send(lead.Token, SendArgs{To: "p1", Body: "again"}))
	eventually(t, e, "p1 resumed", func() bool { return claude.count() == 2 })
	again := claude.last()
	if !again.Resume || again.SessionRef != first.SessionRef || first.Resume {
		t.Fatalf("first %+v, again %+v", first, again)
	}
}
