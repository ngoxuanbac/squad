package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bacnx/squad/internal/core"
	"github.com/bacnx/squad/internal/proto"
)

func TestClaudeHookEvent(t *testing.T) {
	in := claudeHookInput{SessionID: "s", PromptID: "p", Cwd: "/w", Source: "clear", Model: "m"}
	cases := []struct {
		hook  string
		mod   func(*claudeHookInput)
		event string
		ok    bool
	}{
		{"SessionStart", nil, core.HarnessSessionStart, true},
		{"UserPromptSubmit", nil, core.HarnessTurnStart, true},
		{"PostToolUse", nil, core.HarnessToolBoundary, true},
		{"Stop", nil, core.HarnessTurnEnd, true},
		{"StopFailure", nil, core.HarnessTurnEnd, true},
		{"PermissionRequest", nil, core.HarnessPermission, true},
		{"Notification", func(i *claudeHookInput) { i.Notification = "idle_prompt" }, core.HarnessIdle, true},
		{"Notification", func(i *claudeHookInput) { i.Notification = "permission_prompt" }, "", false},
		{"SessionEnd", nil, core.HarnessSessionEnd, true},
		{"SessionEnd", func(i *claudeHookInput) { i.Reason = "clear" }, "", false},
		{"PostToolUse", func(i *claudeHookInput) { i.AgentID = "sub" }, "", false}, // inside a subagent
		{"PermissionRequest", func(i *claudeHookInput) { i.AgentID = "sub" }, core.HarnessPermission, true},
		{"SubagentStop", nil, "", false},
		{"Stop", func(i *claudeHookInput) { i.SessionID = "" }, core.HarnessTurnEnd, false}, // no session id
	}
	for _, c := range cases {
		i := in
		if c.mod != nil {
			c.mod(&i)
		}
		a, ok := claudeHookEvent(c.hook, i)
		if ok != c.ok || (ok && a.Event != c.event) {
			t.Errorf("%s %+v: %+v %v", c.hook, i, a, ok)
		}
	}
	a, _ := claudeHookEvent("Stop", in)
	if !a.OK || a.Key != "s" || a.PromptID != "p" || a.Harness != "claude" {
		t.Errorf("Stop: %+v", a)
	}
	if a, _ := claudeHookEvent("StopFailure", in); a.OK {
		t.Error("StopFailure is not ok")
	}
	if a, _ := claudeHookEvent("SessionStart", in); a.Source != "clear" || a.Cwd != "/w" || a.Model != "m" {
		t.Errorf("SessionStart: %+v", a)
	}
}

func TestClaudeHookOutput(t *testing.T) {
	text := core.HarnessEventResult{Text: "[squad] mail"}
	if claudeHookOutput("Stop", core.HarnessEventResult{}) != nil || claudeHookOutput("Stop", text) != nil {
		t.Fatal("Stop without block printed something")
	}
	stop, _ := json.Marshal(claudeHookOutput("Stop", core.HarnessEventResult{Text: "m", Block: true}))
	if string(stop) != `{"decision":"block","reason":"m"}` {
		t.Fatal(string(stop))
	}
	for _, h := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse"} {
		b, _ := json.Marshal(claudeHookOutput(h, text))
		if !strings.Contains(string(b), `"hookEventName":"`+h+`"`) || !strings.Contains(string(b), `"additionalContext":"[squad] mail"`) {
			t.Errorf("%s: %s", h, b)
		}
	}
	if claudeHookOutput("PermissionRequest", text) != nil {
		t.Fatal("PermissionRequest printed something")
	}
}

func hookRun(t *testing.T, hook string, in any, env map[string]string) string {
	t.Helper()
	b, _ := json.Marshal(in)
	var out bytes.Buffer
	if err := runHook("claude", hook, bytes.NewReader(b), &out, func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// A whole turn through the hook command against a live daemon, as Claude runs it.
func TestRunHookTurn(t *testing.T) {
	daemon(t)
	log := filepath.Join(t.TempDir(), "hooks.log")
	env := map[string]string{"SQUAD_HOOK_LOG": log}
	cwd := t.TempDir()
	hookRun(t, "SessionStart", map[string]any{"session_id": "S", "cwd": cwd, "source": "startup"}, env)
	other := session(t, t.TempDir())
	var me struct{ Name string }
	if err := call(context.Background(), proto.VerbRegister, "", proto.Args{Key: "S", Harness: "claude"}, &me); err != nil {
		t.Fatal(err)
	}
	var sent string
	if err := call(context.Background(), proto.VerbSend, other, core.SendArgs{To: me.Name, Body: "for the session"}, &sent); err != nil {
		t.Fatal(err)
	}
	out := hookRun(t, "UserPromptSubmit", map[string]any{"session_id": "S", "prompt_id": "P"}, env)
	if !strings.Contains(out, `"additionalContext"`) || !strings.Contains(out, "for the session") {
		t.Fatalf("turn start: %s", out)
	}
	if err := call(context.Background(), proto.VerbSend, other, core.SendArgs{To: me.Name, Body: "late"}, &sent); err != nil {
		t.Fatal(err)
	}
	if out := hookRun(t, "Stop", map[string]any{"session_id": "S", "prompt_id": "P"}, env); !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("stop with late mail: %s", out)
	}
	if out := hookRun(t, "Stop", map[string]any{"session_id": "S", "prompt_id": "P"}, env); out != "" {
		t.Fatalf("stop: %s", out)
	}
	if out := hookRun(t, "PreToolUse", map[string]any{"session_id": "S", "tool_name": "mcp__squad__send"}, env); !strings.Contains(out, `"permissionDecision":"allow"`) {
		t.Fatalf("PreToolUse of a squad tool: %s", out)
	}
	if out := hookRun(t, "PreToolUse", map[string]any{"session_id": "S", "tool_name": "Bash"}, env); out != "" {
		t.Fatalf("PreToolUse of another tool: %s", out)
	}
	if out := hookRun(t, "Notification", map[string]any{"session_id": "S", "notification_type": "auth"}, env); out != "" {
		t.Fatal(out)
	}
	b, _ := os.ReadFile(log)
	if strings.Count(string(b), "\n") != 7 || !strings.HasPrefix(string(b), "SessionStart {") {
		t.Fatalf("hook log:\n%s", b)
	}
	// A worker's hooks do nothing; an unknown harness is a usage error.
	if out := hookRun(t, "UserPromptSubmit", map[string]any{"session_id": "S", "prompt_id": "Q"}, map[string]string{"SQUAD_WORKER": "1"}); out != "" {
		t.Fatal(out)
	}
	if err := runHook("codex", "Stop", strings.NewReader("{}"), io.Discard, os.Getenv); err == nil {
		t.Fatal("codex hooks accepted")
	}
	if code, _, errOut := squad(t, "hook", "claude"); code != 1 || !strings.Contains(errOut, "usage") {
		t.Fatalf("hook usage: %d %s", code, errOut)
	}
}

// Without a daemon a hook prints nothing and does not fail.
func TestRunHookNoDaemon(t *testing.T) {
	home(t)
	if out := hookRun(t, "UserPromptSubmit", map[string]any{"session_id": "S", "prompt_id": "P"}, nil); out != "" {
		t.Fatal(out)
	}
}

// memSocket stands in for Claude's messaging socket.
type memSocket struct {
	mu    sync.Mutex
	lines []string
	fail  error
}

func (m *memSocket) dial(path string) (io.WriteCloser, error) {
	if m.fail != nil {
		return nil, m.fail
	}
	return &memConn{m: m, path: path}, nil
}

type memConn struct {
	m    *memSocket
	path string
	buf  bytes.Buffer
}

func (c *memConn) Write(b []byte) (int, error) { return c.buf.Write(b) }
func (c *memConn) Close() error {
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	c.m.lines = append(c.m.lines, strings.Split(strings.TrimSpace(c.buf.String()), "\n")...)
	return nil
}
func (m *memSocket) got() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string{}, m.lines...)
}

func TestNudge(t *testing.T) {
	sock := &memSocket{}
	old := dialMessaging
	dialMessaging = sock.dial
	t.Cleanup(func() { dialMessaging = old }) // after the squad mcp below has stopped
	env := map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": `\\.\pipe\x`, "CLAUDE_CODE_MESSAGING_TOKEN": "tk"}
	s := &mcpServer{getenv: func(k string) string { return env[k] }}
	if err := s.nudge(); err != nil {
		t.Fatal(err)
	}
	if err := s.nudge(); err != nil {
		t.Fatal(err)
	}
	lines := sock.got()
	if len(lines) != 4 || lines[0] != `{"token":"tk","type":"auth"}` || !strings.Contains(lines[1], "[squad] wake #1") ||
		!strings.Contains(lines[3], "[squad] wake #2") || !strings.Contains(lines[1], `"type":"user"`) {
		t.Fatalf("lines: %q", lines)
	}
	delete(env, "CLAUDE_CODE_MESSAGING_TOKEN")
	_ = s.nudge()
	if lines := sock.got(); len(lines) != 5 || strings.Contains(lines[4], "auth") {
		t.Fatalf("no token: %q", lines)
	}
	sock.fail = errors.New("refused")
	if err := s.nudge(); err == nil {
		t.Fatal("dial error lost")
	}
	delete(env, "CLAUDE_CODE_MESSAGING_SOCKET")
	if err := s.nudge(); err == nil {
		t.Fatal("no socket accepted")
	}
}

// A hooked session's squad mcp nudges Claude when mail comes for it while it is idle.
func TestMCPWakesIdleSession(t *testing.T) {
	daemon(t)
	sock := &memSocket{}
	old := dialMessaging
	dialMessaging = sock.dial
	t.Cleanup(func() { dialMessaging = old }) // after the squad mcp below has stopped
	env := map[string]string{"CLAUDE_CODE_SESSION_ID": "W", "CLAUDE_PROJECT_DIR": t.TempDir(), "CLAUDE_CODE_MESSAGING_SOCKET": "sock"}
	c := startMCP(t, env)
	who, _ := c.tool("who", nil)
	name := strings.TrimSuffix(strings.Fields(who)[2], ",")
	hookRun(t, "SessionStart", map[string]any{"session_id": "W", "source": "startup"}, nil)
	other := session(t, t.TempDir())
	var sent string
	if err := call(context.Background(), proto.VerbSend, other, core.SendArgs{To: name, Body: "wake up"}, &sent); err != nil {
		t.Fatal(err)
	}
	if !waitFor(func() bool { return len(sock.got()) > 0 }) {
		t.Fatal("no nudge")
	}
	if !strings.Contains(sock.got()[0], "[squad] wake #1") {
		t.Fatal(sock.got())
	}
}
