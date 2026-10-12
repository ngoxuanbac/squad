package local

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bacnx/squad/internal/testdata/fake"

	"github.com/bacnx/squad/internal/driver"
)

var fakeClaude string

func TestMain(m *testing.M) {
	var done func()
	fakeClaude, done = fake.Build()
	code := m.Run()
	done()
	os.Exit(code)
}

func TestWorkerEnvDropsSessionVars(t *testing.T) {
	in := []string{"PATH=/bin", "CLAUDE_CODE_SESSION_ID=abc", "ANTHROPIC_BASE_URL=http://x", "PASEO_AGENT_ID=1",
		"claudecode=1", "CLAUDE_CODE_USE_BEDROCK=1", "SQUAD_TOKEN=t"}
	got := workerEnv(in)
	want := []string{"PATH=/bin", "ANTHROPIC_BASE_URL=http://x", "CLAUDE_CODE_USE_BEDROCK=1"}
	if !slices.Equal(got, want) {
		t.Fatalf("workerEnv = %v, want %v", got, want)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProfileDefaultsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	// No file: the defaults.
	p, err := ReadClaudeProfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Cmd != "claude" || !slices.Contains(p.DisallowedTools, "AskUserQuestion") {
		t.Fatalf("defaults: %+v", p)
	}
	if m, th := NewClaude(dir, "squad").Defaults(); m != "" || th != "" {
		t.Fatalf("inherit defaults gave %q %q", m, th)
	}
	// EnsureProfiles writes the defaults once and never overwrites.
	if err := EnsureProfiles(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(ClaudeProfilePath(dir)); !strings.Contains(string(b), `"cmd": "claude"`) || !strings.Contains(string(b), `"model": "inherit"`) {
		t.Fatalf("written profile %s", b)
	}
	writeJSON(t, ClaudeProfilePath(dir), map[string]any{"model": "haiku", "thinking": "low", "blacklist": []string{"x"}})
	if err := EnsureProfiles(dir); err != nil {
		t.Fatal(err)
	}
	p, err = ReadClaudeProfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Keys it leaves out keep their defaults; the ones it has win.
	if p.Cmd != "claude" || len(p.DisallowedTools) == 0 || p.Model != "haiku" || !slices.Equal(p.Blacklist, []string{"x"}) {
		t.Fatalf("override: %+v", p)
	}
	if m, th := NewClaude(dir, "squad").Defaults(); m != "haiku" || th != "low" {
		t.Fatalf("Defaults %q %q", m, th)
	}
	// A broken profile is an error, and Defaults falls back to none.
	if err := os.WriteFile(ClaudeProfilePath(dir), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadClaudeProfile(dir); err == nil {
		t.Fatal("broken profile read")
	}
	if m, _ := NewClaude(dir, "squad").Defaults(); m != "" {
		t.Fatal("Defaults of a broken profile")
	}
}

func TestClaudeArgs(t *testing.T) {
	p := DefaultClaudeProfile
	p.Args = []string{"--extra"}
	s := driver.Spec{SessionRef: "sid", Model: "haiku", Thinking: "high", RoleCard: "card", AllowTools: []string{"Monitor"}}
	args := claudeArgs(s, p, "mcp.json", "settings.json")
	joined := strings.Join(args, " ")
	for _, want := range []string{"--session-id sid", "--model haiku", "--effort high", "--mcp-config mcp.json",
		"--strict-mcp-config", "--setting-sources user", "--permission-mode bypassPermissions"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args lack %q: %s", want, joined)
		}
	}
	if args[len(args)-1] != "--extra" {
		t.Errorf("profile args not last: %v", args)
	}
	i := slices.Index(args, "--disallowedTools")
	if i < 0 || strings.Contains(args[i+1], "Monitor") || !strings.Contains(args[i+1], "AskUserQuestion") {
		t.Errorf("disallowed tools: %v", args)
	}
	i = slices.Index(args, "--append-system-prompt")
	if i < 0 || !strings.HasPrefix(args[i+1], "card") || !strings.Contains(args[i+1], "[squad]") {
		t.Errorf("role card: %v", args)
	}

	s.Resume, s.Model, s.Thinking = true, "", ""
	p.DisallowedTools = nil
	args = claudeArgs(s, p, "m", "settings.json")
	joined = strings.Join(args, " ")
	if !strings.Contains(joined, "--resume sid") || strings.Contains(joined, "--session-id") ||
		strings.Contains(joined, "--model") || strings.Contains(joined, "--effort") ||
		strings.Contains(joined, "--disallowedTools") || !strings.Contains(joined, "--settings settings.json") {
		t.Errorf("resume args: %s", joined)
	}
}

func TestMCPConfigMergesUserServers(t *testing.T) {
	dir := t.TempDir()
	c := NewClaude(dir, "/bin/squad")
	c.UserConfig = filepath.Join(t.TempDir(), ".claude.json")
	writeJSON(t, c.UserConfig, map[string]any{"mcpServers": map[string]any{
		"github": map[string]any{"command": "gh-mcp"},
		"secret": map[string]any{"command": "no"},
		"squad":  map[string]any{"command": "old"}, // the user-scope one setup claude added
	}})
	b, err := c.mcpConfig([]string{"secret"}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		McpServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.McpServers["secret"]; ok {
		t.Error("blacklisted server kept")
	}
	if got.McpServers["github"].Command != "gh-mcp" {
		t.Error("user server lost")
	}
	sq := got.McpServers["squad"]
	if sq.Command != "/bin/squad" || sq.Env["SQUAD_TOKEN"] != "tok" || sq.Env["SQUAD_HOME"] != dir || !slices.Equal(sq.Args, []string{"mcp"}) {
		t.Errorf("squad server: %+v", sq)
	}

	// No ~/.claude.json: squad alone. A broken one: an error.
	c.UserConfig = filepath.Join(t.TempDir(), "none.json")
	if b, err := c.mcpConfig(nil, "t"); err != nil || !strings.Contains(string(b), `"squad"`) {
		t.Fatalf("missing user config: %s %v", b, err)
	}
	if err := os.WriteFile(c.UserConfig, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.mcpConfig(nil, "t"); err == nil {
		t.Fatal("broken user config accepted")
	}
}

func TestPluginsOff(t *testing.T) {
	if got := pluginsOff([]string{"server-only"}); len(got) != 1 || got[ClaudeSquadPlugin] {
		t.Fatalf("only squad's own plugin is off: %v", got)
	}
	got := pluginsOff([]string{"b@m", "server", "a@m"})
	if len(got) != 3 || got["a@m"] || got["b@m"] || got[ClaudeSquadPlugin] {
		t.Fatalf("pluginsOff = %v", got)
	}
}

// recorder is driver.Events that keeps what a run reported.
type recorder struct {
	mu        sync.Mutex
	working   []bool
	completed map[int64]bool
	exited    chan error
}

func newRecorder() *recorder {
	return &recorder{completed: map[int64]bool{}, exited: make(chan error, 1)}
}

func (r *recorder) Working(_ string, w bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.working = append(r.working, w)
}

func (r *recorder) Completed(_ string, batch int64, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completed[batch] = ok
}

func (r *recorder) Exited(_ string, err error) { r.exited <- err }

func (r *recorder) batch(b int64) (ok, seen bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ok, seen = r.completed[b]
	return
}

func startFake(t *testing.T, prof ClaudeProfile) (*Claude, driver.Spec) {
	t.Helper()
	dir := t.TempDir()
	prof.Cmd = fakeClaude
	writeJSON(t, ClaudeProfilePath(dir), prof)
	c := NewClaude(dir, "squad")
	c.UserConfig = filepath.Join(dir, "none.json")
	run := filepath.Join(dir, "run", "p1")
	return c, driver.Spec{ParticipantID: "p1", Cwd: dir, SessionRef: c.NewSessionRef(), RoleCard: "card",
		Token: "tok", LogPath: filepath.Join(dir, "p1.jsonl"), RunDir: run}
}

func TestStartDeliverStop(t *testing.T) {
	c, s := startFake(t, ClaudeProfile{Blacklist: []string{"p@m"}, Env: []string{"EXTRA=1"}})
	ev := newRecorder()
	r, err := c.Start(s, ev)
	if err != nil {
		t.Fatal(err)
	}
	if r.Pid() <= 0 {
		t.Fatal("no pid")
	}
	if err := r.Deliver(7, "[squad] hello"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ok, seen := ev.batch(7); seen {
			if !ok {
				t.Fatal("batch 7 cancelled")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("batch 7 never completed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := r.Interrupt(); err != nil {
		t.Fatal(err)
	}
	r.Stop(2 * time.Second)
	select {
	case <-r.(*claudeRun).done:
	default:
		t.Fatal("Done not closed after Stop")
	}
	<-ev.exited
	if err := r.Deliver(8, "late"); err == nil {
		t.Fatal("deliver to an exited worker")
	}
	// The run wrote its MCP config, its settings (a blacklisted plugin) and its log.
	for _, f := range []string{filepath.Join(s.RunDir, "mcp.json"), filepath.Join(s.RunDir, "settings.json"), s.LogPath} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	log, _ := os.ReadFile(s.LogPath)
	if !strings.Contains(string(log), `"squad_mail"`) || !strings.Contains(string(log), `"command_lifecycle"`) {
		t.Errorf("log lacks mail or output: %s", log)
	}
}

func TestStartErrors(t *testing.T) {
	c, s := startFake(t, ClaudeProfile{})
	writeJSON(t, ClaudeProfilePath(c.dir), map[string]any{"cmd": filepath.Join(t.TempDir(), "no-such-claude")})
	if _, err := c.Start(s, newRecorder()); err == nil {
		t.Fatal("start with a missing command")
	}
	if err := os.WriteFile(ClaudeProfilePath(c.dir), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Start(s, newRecorder()); err == nil {
		t.Fatal("start with a broken profile")
	}
	c2, s2 := startFake(t, ClaudeProfile{})
	s2.LogPath = filepath.Join(t.TempDir(), "missing", "dir", "log")
	if _, err := c2.Start(s2, newRecorder()); err == nil {
		t.Fatal("start with an unwritable log")
	}
}

func TestHandleLines(t *testing.T) {
	ev := newRecorder()
	r := &claudeRun{id: "p", batches: map[string]int64{"u1": 1, "u2": 2}}
	for _, l := range []string{
		`{"type":"system","subtype":"session_state_changed","state":"running"}`,
		`{"type":"command_lifecycle","state":"started","command_uuid":"u1"}`,
		`{"type":"result"}`,
		`{"type":"command_lifecycle","state":"completed","command_uuid":"u1"}`,
		`{"type":"command_lifecycle","state":"cancelled","command_uuid":"u2"}`,
		`{"type":"command_lifecycle","state":"completed","command_uuid":"unknown"}`,
		`not json`,
	} {
		r.handle([]byte(l), ev)
	}
	if !slices.Equal(ev.working, []bool{true, true, false}) {
		t.Errorf("working %v", ev.working)
	}
	if ok, seen := ev.batch(1); !seen || !ok {
		t.Error("batch 1 not completed")
	}
	if ok, seen := ev.batch(2); !seen || ok {
		t.Error("batch 2 not cancelled")
	}
	if len(ev.completed) != 2 || len(r.batches) != 0 {
		t.Errorf("completed %v, left %v", ev.completed, r.batches)
	}
}

func TestAliveAndHarness(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Error("this process is not alive")
	}
	if Alive(0) || Alive(-1) {
		t.Error("pid 0 or -1 alive")
	}
	c := NewClaude(t.TempDir(), "squad")
	if a, b := c.NewSessionRef(), c.NewSessionRef(); c.Harness() != ClaudeHarness || a == b {
		t.Error("harness or session refs")
	}
}

// A worker that ignores its stdin closing is killed with its whole tree within the grace period.
func TestStopKillsAHungWorker(t *testing.T) {
	c, s := startFake(t, ClaudeProfile{Env: []string{"FAKE_HANG=1"}})
	ev := newRecorder()
	r, err := c.Start(s, ev)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	r.Stop(time.Second)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("stop took %v", took)
	}
	select {
	case <-ev.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("no exit reported")
	}
	if Alive(r.Pid()) {
		t.Fatal("hung worker still alive")
	}
}

// Detach starts a process that outlives its parent; it runs and exits on its own here.
func TestDetach(t *testing.T) {
	for _, detach := range []func(*exec.Cmd){Detach, DetachNoBreakaway} {
		cmd := exec.Command(fakeClaude)
		detach(cmd)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err != nil {
			t.Fatalf("detached fake: %v", err)
		}
	}
}

// Configure waits for Claude's control_response; the fake refuses "bad".
func TestConfigure(t *testing.T) {
	c, s := startFake(t, ClaudeProfile{})
	ev := newRecorder()
	run, err := c.Start(s, ev)
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Configure("sonnet", "high"); err != nil {
		t.Fatal(err)
	}
	if err := run.Configure("bad", ""); err == nil || !strings.Contains(err.Error(), "model bad: not available") {
		t.Fatalf("bad model: %v", err)
	}
	if err := run.Configure("", "bad"); err == nil || !strings.Contains(err.Error(), "thinking bad: not available") {
		t.Fatalf("bad thinking: %v", err)
	}
	if err := run.Configure("opus", "bad"); err == nil || !strings.Contains(err.Error(), "the model is now opus") {
		t.Fatalf("model then bad thinking: %v", err)
	}
	run.Stop(2 * time.Second)
	<-ev.exited
	if err := run.Configure("opus", ""); err == nil {
		t.Fatal("configured an exited worker")
	}

	// No answer: it gives up after controlTimeout; an unknown answer is ignored.
	old := controlTimeout
	controlTimeout = 50 * time.Millisecond
	t.Cleanup(func() { controlTimeout = old })
	r := &claudeRun{id: "p", pending: map[string]chan error{}, done: make(chan struct{}), outSig: make(chan struct{}, 1)}
	if err := r.control(map[string]any{"subtype": "set_model"}); err == nil || !strings.Contains(err.Error(), "no answer") {
		t.Fatalf("no answer: %v", err)
	}
	r.handle([]byte(`{"type":"control_response","response":{"subtype":"success","request_id":"unknown"}}`), newRecorder())
}
