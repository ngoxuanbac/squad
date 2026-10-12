// fakeclaude speaks enough of `claude -p` stream-json for squad's tests. Every user message is a
// turn; the lines of it that start with @ are its work, done in order:
//
//	@send|inbox|who|agent <json>  call that squad tool on the MCP server of --mcp-config
//	@sleep <duration>             work that long (an interrupt cancels the turn)
//	@exit <code>                  die in the middle of the turn
//	@await <key>                  work until the file $FAKE_STATE/<key> exists (at most a minute)
//	@once <key> <directive>       the directive, only the first time any run sees key
//	@permission                   ask its person for a permission (a hooked session)
//	@esc                          its person presses Esc: the turn ends there
//
// A tool call is logged as Claude logs one: a tool_use, then its tool_result; a tool's arguments
// are checked against its schema as Claude does. With FAKE_HOOKS=1 it is a session with squad's
// hooks plugin (hooks.go). Run as claude or paseo (the cli tests copy it under both names), it also
// answers the few commands squad setup runs (--version, mcp …, plugin …).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

var out = json.NewEncoder(os.Stdout)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version":
			fmt.Println("2.1.289 (Claude Code)")
			return
		case "mcp":
			return
		case "plugin":
			switch strings.Join(os.Args[2:], " ") {
			case "ls --json": // paseo
				fmt.Println(`[{"id":"squad","status":"running"}]`)
			case "list --json": // claude
				fmt.Println(`[{"id":"squad@squad","enabled":true}]`)
			case "marketplace list --json":
				fmt.Println(`[{"name":"squad","installLocation":"/x"}]`)
			}
			return
		}
	}
	mark("worker", os.Getpid())
	in := make(chan []byte) // stdin's lines, nil at its end; and the messaging socket's prompts
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 64<<10), 16<<20)
		for sc.Scan() {
			in <- slices.Clone(sc.Bytes())
		}
		in <- nil
	}()
	t := &turn{lines: in, hooks: startHooks(in)}
	if t.hooks != nil { // Claude starts its MCP servers with the session
		t.server, _ = dialMCP()
	}
	// Like Claude, session_state_changed only with CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1.
	state := os.Getenv("CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS") == "1"
	for {
		m := t.next()
		if m.Type == "control_request" {
			t.control(m)
			continue
		}
		if m.Type != "user" {
			continue
		}
		if state {
			out.Encode(map[string]any{"type": "system", "subtype": "session_state_changed", "state": "running"})
		}
		out.Encode(map[string]any{"type": "command_lifecycle", "command_uuid": m.UUID, "state": "started"})
		if t.whole(m) {
			out.Encode(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}})
			out.Encode(map[string]any{"type": "result", "subtype": "success", "duration_ms": 5, "result": "ok"})
			out.Encode(map[string]any{"type": "command_lifecycle", "command_uuid": m.UUID, "state": "completed"})
		} else {
			out.Encode(map[string]any{"type": "result", "subtype": "error_during_execution", "duration_ms": 5})
			out.Encode(map[string]any{"type": "command_lifecycle", "command_uuid": m.UUID, "state": "cancelled"})
		}
		if state {
			out.Encode(map[string]any{"type": "system", "subtype": "session_state_changed", "state": "idle"})
		}
	}
}

type msg struct {
	Type    string `json:"type"`
	UUID    string `json:"uuid"`
	ID      string `json:"request_id"`
	Request struct {
		Subtype  string         `json:"subtype"`
		Model    string         `json:"model"`
		Settings map[string]any `json:"settings"`
	} `json:"request"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// text is a user message's text: its content is a string or text blocks.
func (m msg) text() string {
	var s string
	if json.Unmarshal(m.Message.Content, &s) == nil {
		return s
	}
	var blocks []struct{ Text string }
	_ = json.Unmarshal(m.Message.Content, &blocks)
	var b strings.Builder
	for _, x := range blocks {
		b.WriteString(x.Text + "\n")
	}
	return b.String()
}

// turn reads stdin: a user message that comes during a turn waits for the next one.
type turn struct {
	lines  <-chan []byte
	queue  []msg
	server *mcpClient
	calls  int
	hooks  *hooks
	prompt string // the turn's prompt id
}

func (t *turn) next() msg {
	if len(t.queue) > 0 {
		m := t.queue[0]
		t.queue = t.queue[1:]
		return m
	}
	for {
		if m, ok := t.read(<-t.lines); ok {
			return m
		}
	}
}

// read decodes a line of stdin; nil is its end, and the run ends with it.
func (t *turn) read(line []byte) (msg, bool) {
	if line == nil {
		t.closed()
	}
	var m msg
	return m, json.Unmarshal(line, &m) == nil
}

// closed: stdin is closed, so the session or worker ends.
func (t *turn) closed() {
	fmt.Fprintln(os.Stderr, "fakeclaude: stdin closed")
	if t.hooks != nil {
		t.hooks.run("SessionEnd", map[string]any{"reason": "prompt_input_exit"})
	}
	if os.Getenv("FAKE_HANG") == "1" && os.Getenv("SQUAD_WORKER") == "1" { // a worker that does not end when its stdin closes
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

// whole runs a turn from its prompt to its end: false when it was cancelled. Hooked, the turn's
// mail comes with its start and after each tool call, and its end is held back while mail came.
func (t *turn) whole(m msg) bool {
	t.prompt = m.UUID
	text := m.text()
	if t.hooks == nil {
		return t.run(text)
	}
	text += "\n" + context(t.hooks.run("UserPromptSubmit", map[string]any{"prompt_id": t.prompt, "prompt": text}))
	for t.run(text) {
		res := t.hooks.run("Stop", map[string]any{"prompt_id": t.prompt})
		if res["decision"] != "block" {
			return true
		}
		text, _ = res["reason"].(string)
	}
	// Esc or an interrupt: Claude waits on its person.
	t.hooks.run("Notification", map[string]any{"notification_type": "idle_prompt"})
	return false
}

// control answers a control request; a model or effort named "bad" is refused. It reports an
// interrupt.
func (t *turn) control(m msg) bool {
	resp := map[string]any{"subtype": "success", "request_id": m.ID}
	if m.Request.Model == "bad" || m.Request.Settings["effortLevel"] == "bad" {
		resp = map[string]any{"subtype": "error", "request_id": m.ID, "error": "not available"}
	}
	out.Encode(map[string]any{"type": "control_response", "response": resp})
	return m.Request.Subtype == "interrupt"
}

// wait works for d: false when an interrupt cancelled the turn.
func (t *turn) wait(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return true
		case line := <-t.lines:
			m, ok := t.read(line)
			if !ok {
				continue
			}
			if m.Type != "control_request" {
				t.queue = append(t.queue, m)
			} else if t.control(m) {
				return false
			}
		}
	}
}

// run does the work in text: false when the turn was cancelled.
func (t *turn) run(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "@") && !t.do(line[1:]) {
			return false
		}
	}
	return true
}

func (t *turn) do(directive string) bool {
	verb, arg, _ := strings.Cut(directive, " ")
	arg = strings.TrimSpace(arg)
	switch verb {
	case "sleep":
		d, _ := time.ParseDuration(arg)
		return t.wait(d)
	case "exit":
		code, _ := strconv.Atoi(arg)
		os.Exit(code)
	case "await":
		for end := time.Now().Add(time.Minute); time.Now().Before(end); {
			if _, err := os.Stat(stateFile(arg)); err == nil {
				return true
			}
			if !t.wait(50 * time.Millisecond) {
				return false
			}
		}
	case "once":
		key, rest, _ := strings.Cut(arg, " ")
		f, err := os.OpenFile(stateFile(key), os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return true
		}
		f.Close()
		return t.do(strings.TrimPrefix(strings.TrimSpace(rest), "@"))
	case "permission":
		if t.hooks != nil {
			t.hooks.run("PermissionRequest", map[string]any{"tool_name": "Bash"})
		}
	case "esc":
		return false
	case "send", "inbox", "who", "agent":
		return t.tool(verb, arg)
	}
	return true
}

func stateFile(key string) string { return filepath.Join(os.Getenv("FAKE_STATE"), key) }

// mark leaves $FAKE_STATE/pid-<what>-<pid> for a worker's processes (itself, its MCP server), so
// a test can see that none outlives what should end it.
func mark(what string, pid int) {
	if os.Getenv("SQUAD_WORKER") == "1" && os.Getenv("FAKE_STATE") != "" {
		_ = os.WriteFile(stateFile(fmt.Sprintf("pid-%s-%d", what, pid)), nil, 0o600)
	}
}

// tool calls a squad tool and logs the call and its result; hooked, the mail given after it is
// worked on too.
func (t *turn) tool(name, args string) bool {
	t.calls++
	id := fmt.Sprintf("toolu_%d", t.calls)
	if args == "" {
		args = "{}"
	}
	input := json.RawMessage(args)
	out.Encode(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{
		map[string]any{"type": "tool_use", "id": id, "name": "mcp__squad__" + name, "input": input}}}})
	text, isErr := t.call(name, input)
	out.Encode(map[string]any{"type": "user", "message": map[string]any{"content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": id, "content": text, "is_error": isErr}}}})
	if t.hooks == nil {
		return true
	}
	return t.run(context(t.hooks.run("PostToolUse", map[string]any{"prompt_id": t.prompt, "tool_name": "mcp__squad__" + name})))
}

func (t *turn) call(name string, input json.RawMessage) (string, bool) {
	if !json.Valid(input) {
		return "bad directive arguments: " + string(input), true
	}
	if t.server == nil {
		c, err := dialMCP()
		if err != nil {
			return "mcp: " + err.Error(), true
		}
		t.server = c
	}
	if err := t.server.check(name, input); err != nil {
		return "InputValidationError: " + err.Error(), true
	}
	raw, err := t.server.call("tools/call", map[string]any{"name": name, "arguments": input})
	if err != nil {
		return "mcp: " + err.Error(), true
	}
	var r struct {
		Content []struct{ Text string }
		IsError bool
	}
	_ = json.Unmarshal(raw, &r)
	var b strings.Builder
	for _, c := range r.Content {
		b.WriteString(c.Text)
	}
	return b.String(), r.IsError
}
