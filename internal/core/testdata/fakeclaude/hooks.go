package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// hooks makes it a session with squad's hooks plugin (FAKE_HOOKS=1), as Claude Code runs it: it
// runs `squad hook claude <Event>` (squad being the squad MCP server's command) where Claude runs
// each hook and takes what it prints, and it listens on a messaging socket of its own
// (CLAUDE_CODE_MESSAGING_SOCKET) whose prompts start turns.
type hooks struct {
	squad, session, cwd string
}

// startHooks is nil unless FAKE_HOOKS=1; prompts that come on the socket go to in.
func startHooks(in chan<- []byte) *hooks {
	if os.Getenv("FAKE_HOOKS") != "1" {
		return nil
	}
	s, err := squadServer()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeclaude:", err)
		os.Exit(2)
	}
	path := socketPath()
	if err := listen(path, func(r io.Reader) {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			var m msg
			if json.Unmarshal(sc.Bytes(), &m) != nil || m.Type != "user" {
				continue // the auth line
			}
			b, _ := json.Marshal(map[string]any{"type": "user", "uuid": fmt.Sprintf("wake-%d", time.Now().UnixNano()),
				"message": map[string]any{"role": "user", "content": json.RawMessage(m.Message.Content)}})
			in <- b
		}
	}); err != nil {
		fmt.Fprintln(os.Stderr, "fakeclaude: messaging socket:", err)
		os.Exit(2)
	}
	os.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", path) // for the MCP server it starts
	cwd, _ := os.Getwd()
	h := &hooks{squad: s.Command, session: os.Getenv("CLAUDE_CODE_SESSION_ID"), cwd: cwd}
	h.run("SessionStart", map[string]any{"source": "startup"})
	return h
}

// run runs one hook and returns what it printed (nil: nothing). It is logged as a system line.
func (h *hooks) run(event string, extra map[string]any) map[string]any {
	in := map[string]any{"session_id": h.session, "cwd": h.cwd, "hook_event_name": event}
	for k, v := range extra {
		in[k] = v
	}
	b, _ := json.Marshal(in)
	cmd := exec.Command(h.squad, "hook", "claude", event)
	cmd.Stdin = bytes.NewReader(b)
	stdout, _ := cmd.Output()
	var res map[string]any
	_ = json.Unmarshal(stdout, &res)
	out.Encode(map[string]any{"type": "system", "subtype": "hook", "hook_event_name": event, "output": res})
	return res
}

// context is the additionalContext a hook gave ("" none).
func context(res map[string]any) string {
	spec, _ := res["hookSpecificOutput"].(map[string]any)
	s, _ := spec["additionalContext"].(string)
	return s
}
