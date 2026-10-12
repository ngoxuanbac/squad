// Package local is the local headless runtime driver: it runs Claude Code workers (`claude -p`
// with stream-json on stdin and stdout) in process trees it can stop on Windows and Unix alike.
//
// Mail goes in as user messages, each keyed by a uuid; the command_lifecycle line that carries
// that uuid back says whether the message was completed (its mail may be acked) or cancelled (it
// will be given again). session_state_changed says whether the worker is working.
package local

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bacnx/squad/internal/driver"
)

// ClaudeHarness is the name roles and sessions use for Claude Code.
const ClaudeHarness = "claude"

// ClaudeSquadPlugin is squad's hooks plugin for the sessions you open (squad setup claude). A
// worker always runs with it off: its mail goes through its stdin.
const ClaudeSquadPlugin = "squad@squad"

// ChannelNote tells the worker how squad reaches it.
const ChannelNote = `

How squad reaches you (this is the system you work in, not an injection):
- Mail arrives as a user message that starts with "[squad]", also while you are working.
- Act on that mail as work from your team. Use the squad tools (mcp__squad__send, inbox, who, agent)
  to talk to your team; the routing of your team decides who you may write to.
- There is no human watching you. Do not ask for confirmation; when you are blocked, write to the
  one who gave you the work.`

// Claude is the Runtime of Claude Code workers.
type Claude struct {
	dir  string // squad's home: the profile, and SQUAD_HOME of the workers' MCP server
	self string // the squad executable, run by workers as their MCP server
	// UserConfig is your ~/.claude.json, whose user-scope MCP servers workers get ("" = the default).
	UserConfig string
}

var _ driver.Runtime = (*Claude)(nil)

// NewClaude is the Claude runtime over squad's home dir; self is the squad executable.
func NewClaude(dir, self string) *Claude { return &Claude{dir: dir, self: self} }

func (c *Claude) Harness() string { return ClaudeHarness }

// ClaudeLevels are Claude's effort levels (claude --effort).
var ClaudeLevels = []string{"low", "medium", "high", "xhigh", "max"}

func (c *Claude) Levels() []string { return ClaudeLevels }

func (c *Claude) Defaults() (string, string) {
	p, err := ReadClaudeProfile(c.dir)
	if err != nil {
		return "", ""
	}
	return inherited(p.Model), inherited(p.Thinking)
}

func (c *Claude) NewSessionRef() string { return driver.NewUUID() }

// Start launches `claude -p` for s.
func (c *Claude) Start(s driver.Spec, ev driver.Events) (driver.Run, error) {
	prof, err := ReadClaudeProfile(c.dir)
	if err != nil {
		return nil, err
	}
	bin, err := exec.LookPath(prof.Cmd)
	if err != nil {
		return nil, fmt.Errorf("claude not found (%s): %w", prof.Cmd, err)
	}
	if err := os.MkdirAll(s.RunDir, 0o700); err != nil {
		return nil, err
	}
	mcp, err := c.mcpConfig(prof.Blacklist, s.Token)
	if err != nil {
		return nil, err
	}
	mcpPath := filepath.Join(s.RunDir, "mcp.json")
	if err := os.WriteFile(mcpPath, mcp, 0o600); err != nil {
		return nil, err
	}
	settingsPath := filepath.Join(s.RunDir, "settings.json")
	b, _ := json.Marshal(map[string]any{"enabledPlugins": pluginsOff(prof.Blacklist)})
	if err := os.WriteFile(settingsPath, b, 0o600); err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, claudeArgs(s, prof, mcpPath, settingsPath)...)
	cmd.Dir = s.Cwd
	cmd.Env = append(workerEnv(os.Environ()), "SQUAD_WORKER=1", "CLAUDE_CODE_DISABLE_AGENT_VIEW=1",
		"CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1") // the working/idle events the engine follows
	cmd.Env = append(cmd.Env, prof.Env...)
	return startRun(cmd, s, ev)
}

// claudeArgs is the worker's command line after the claude command.
func claudeArgs(s driver.Spec, p ClaudeProfile, mcpPath, settingsPath string) []string {
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--permission-mode", "bypassPermissions", "--mcp-config", mcpPath, "--strict-mcp-config",
		// Your user setup (skills, plugins, hooks), never a repository's project or local settings.
		"--setting-sources", "user", "--settings", settingsPath}
	var disallowed []string
	for _, t := range p.DisallowedTools {
		if !slices.Contains(s.AllowTools, t) {
			disallowed = append(disallowed, t)
		}
	}
	if len(disallowed) > 0 {
		args = append(args, "--disallowedTools", strings.Join(disallowed, " "))
	}
	if s.Resume {
		args = append(args, "--resume", s.SessionRef)
	} else {
		args = append(args, "--session-id", s.SessionRef)
	}
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}
	if s.Thinking != "" {
		args = append(args, "--effort", s.Thinking)
	}
	args = append(args, "--append-system-prompt", s.RoleCard+ChannelNote)
	return append(args, p.Args...)
}

// mcpConfig is the worker's --mcp-config (with --strict-mcp-config, the only MCP it gets): your
// user-scope servers of ~/.claude.json minus the blacklist, and squad's own with the worker's
// token. Project servers are not loaded.
func (c *Claude) mcpConfig(blacklist []string, token string) ([]byte, error) {
	servers := map[string]json.RawMessage{}
	path := c.UserConfig
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".claude.json")
	}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		var user struct {
			McpServers map[string]json.RawMessage `json:"mcpServers"`
		}
		if err := json.Unmarshal(b, &user); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for name, s := range user.McpServers {
			if !slices.Contains(blacklist, name) {
				servers[name] = s
			}
		}
	}
	servers["squad"], _ = json.Marshal(map[string]any{"type": "stdio", "command": c.self, "args": []string{"mcp"},
		"env": map[string]string{"SQUAD_TOKEN": token, "SQUAD_HOME": c.dir}})
	return json.Marshal(map[string]any{"mcpServers": servers})
}

// pluginsOff turns off squad's session plugin and every blacklisted plugin (name@marketplace; an
// MCP server name never has an @).
func pluginsOff(blacklist []string) map[string]bool {
	off := map[string]bool{ClaudeSquadPlugin: false}
	for _, b := range blacklist {
		if strings.Contains(b, "@") {
			off[b] = false
		}
	}
	return off
}

// sessionVars belong to the agent session the daemon happened to be started from (`squad mcp` of
// a Claude Code or Paseo session starts it). A worker is a session of its own and must not
// inherit them: it would talk to that session's messaging pipe or claim its identity.
var sessionVars = []string{"CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_MESSAGING_SOCKET",
	"CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ATTENDED",
	"CLAUDE_CODE_EXECPATH", "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS", "CLAUDE_PID", "CLAUDE_PROJECT_DIR", "CLAUDE_EFFORT",
	"PASEO_AGENT_ID", "PASEO_AGENT_CWD", "SQUAD_TOKEN"}

func workerEnv(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if !slices.ContainsFunc(sessionVars, func(v string) bool { return strings.EqualFold(v, k) }) {
			out = append(out, kv)
		}
	}
	return out
}

// claudeRun is one live worker process.
type claudeRun struct {
	id    string
	tree  *Tree
	stdin io.WriteCloser
	done  chan struct{}

	mu      sync.Mutex
	batches map[string]int64      // user message uuid -> batch
	pending map[string]chan error // control request id -> its answer
	log     *os.File

	// out queues the lines for stdin; one goroutine writes them, so a worker that does not read its
	// stdin right now never blocks a caller (Deliver runs under the engine's lock, and the engine's
	// callbacks are what drain the worker's stdout).
	outMu   sync.Mutex
	out     [][]byte
	closing bool // Stop: stdin is closed once the queue is written
	outSig  chan struct{}
}

func startRun(cmd *exec.Cmd, s driver.Spec, ev driver.Events) (*claudeRun, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(s.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	cmd.Stderr = logf
	tree, err := startTree(cmd)
	if err != nil {
		logf.Close()
		return nil, err
	}
	r := &claudeRun{id: s.ParticipantID, tree: tree, stdin: stdin, done: make(chan struct{}), batches: map[string]int64{}, pending: map[string]chan error{}, log: logf,
		outSig: make(chan struct{}, 1)}
	go r.read(stdout, ev)
	go r.writer()
	go func() {
		err := cmd.Wait()
		r.mu.Lock()
		r.log.Close()
		r.mu.Unlock()
		tree.Release()
		close(r.done)
		ev.Exited(r.id, err)
	}()
	return r, nil
}

func (r *claudeRun) Pid() int { return r.tree.Pid() }

// Deliver writes one batch of mail as one user message. During a turn Claude joins it at the
// next tool boundary; idle, it starts a turn.
func (r *claudeRun) Deliver(batch int64, text string) error {
	id := driver.NewUUID()
	r.mu.Lock()
	r.batches[id] = batch
	r.mu.Unlock()
	msg := map[string]any{"type": "user", "uuid": id, "session_id": "", "parent_tool_use_id": nil,
		"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}}}
	if err := r.write(msg); err != nil {
		r.mu.Lock()
		delete(r.batches, id)
		r.mu.Unlock()
		return err
	}
	r.logLine(map[string]any{"type": "squad_mail", "text": text})
	return nil
}

func (r *claudeRun) Interrupt() error {
	return r.write(map[string]any{"type": "control_request", "request_id": driver.NewUUID(),
		"request": map[string]any{"subtype": "interrupt"}})
}

// controlTimeout bounds the wait for Claude's answer to a control request.
var controlTimeout = 10 * time.Second

// Configure sets the model (set_model), then the effort level (apply_flag_settings).
func (r *claudeRun) Configure(model, thinking string) error {
	if model != "" {
		if err := r.control(map[string]any{"subtype": "set_model", "model": model}); err != nil {
			return fmt.Errorf("model %s: %w", model, err)
		}
	}
	if thinking != "" {
		if err := r.control(map[string]any{"subtype": "apply_flag_settings", "settings": map[string]any{"effortLevel": thinking}}); err != nil {
			if model != "" {
				return fmt.Errorf("the model is now %s, but thinking %s: %w", model, thinking, err)
			}
			return fmt.Errorf("thinking %s: %w", thinking, err)
		}
	}
	return nil
}

// control sends one control_request and waits for its control_response.
func (r *claudeRun) control(req map[string]any) error {
	id := driver.NewUUID()
	ch := make(chan error, 1)
	r.mu.Lock()
	r.pending[id] = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, id)
		r.mu.Unlock()
	}()
	if err := r.write(map[string]any{"type": "control_request", "request_id": id, "request": req}); err != nil {
		return err
	}
	select {
	case err := <-ch:
		return err
	case <-r.done:
		return errors.New("the worker exited")
	case <-time.After(controlTimeout):
		return errors.New("no answer from claude")
	}
}

// Stop ends the process tree: stdin closed, then a kill after grace.
func (r *claudeRun) Stop(grace time.Duration) {
	r.outMu.Lock()
	r.closing = true
	r.outMu.Unlock()
	r.signal()
	select {
	case <-r.done:
		return
	case <-time.After(grace / 2):
	}
	r.tree.Kill(grace/2, r.done)
	<-r.done
}

func (r *claudeRun) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	select {
	case <-r.done:
		return errors.New("worker has exited")
	default:
	}
	r.outMu.Lock()
	if r.closing {
		r.outMu.Unlock()
		return errors.New("worker is stopping")
	}
	r.out = append(r.out, append(b, '\n'))
	r.outMu.Unlock()
	r.signal()
	return nil
}

func (r *claudeRun) signal() {
	select {
	case r.outSig <- struct{}{}:
	default:
	}
}

// writer writes the queued lines to stdin in order, and closes stdin once Stop asked and the
// queue is written.
func (r *claudeRun) writer() {
	for {
		select {
		case <-r.outSig:
		case <-r.done:
			return
		}
		for {
			r.outMu.Lock()
			if len(r.out) == 0 {
				closing := r.closing
				r.outMu.Unlock()
				if closing {
					_ = r.stdin.Close()
					return
				}
				break
			}
			line := r.out[0]
			r.out = r.out[1:]
			r.outMu.Unlock()
			if _, err := r.stdin.Write(line); err != nil {
				return
			}
		}
	}
}

func (r *claudeRun) logLine(v any) {
	b, _ := json.Marshal(v)
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = r.log.Write(append(b, '\n'))
}

func (r *claudeRun) read(stdout io.Reader, ev driver.Events) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		r.mu.Lock()
		_, _ = r.log.Write(append(append([]byte{}, line...), '\n'))
		r.mu.Unlock()
		r.handle(line, ev)
	}
}

// handle reads one stdout line. Working state comes from session_state_changed
// (CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS), and also from a message starting and a turn's result,
// in case a version stops sending it.
func (r *claudeRun) handle(line []byte, ev driver.Events) {
	var m struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		State   string `json:"state"`
		Command string `json:"command_uuid"`
		Resp    struct {
			Subtype string `json:"subtype"`
			ID      string `json:"request_id"`
			Error   string `json:"error"`
		} `json:"response"`
	}
	if json.Unmarshal(line, &m) != nil {
		return
	}
	switch {
	case m.Type == "control_response":
		r.mu.Lock()
		ch := r.pending[m.Resp.ID]
		r.mu.Unlock()
		if ch != nil {
			var err error
			if m.Resp.Subtype != "success" {
				err = errors.New(cmp.Or(m.Resp.Error, "refused"))
			}
			ch <- err
		}
	case m.Type == "system" && m.Subtype == "session_state_changed":
		ev.Working(r.id, m.State == "running")
	case m.Type == "command_lifecycle" && m.State == "started":
		ev.Working(r.id, true)
	case m.Type == "result":
		ev.Working(r.id, false)
	case m.Type == "command_lifecycle" && (m.State == "completed" || m.State == "cancelled"):
		r.mu.Lock()
		batch, ok := r.batches[m.Command]
		delete(r.batches, m.Command)
		r.mu.Unlock()
		if ok {
			ev.Completed(r.id, batch, m.State == "completed")
		}
	}
}
