package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bacnx/squad/internal/core"
	"github.com/bacnx/squad/internal/driver/local"
	"github.com/bacnx/squad/internal/proto"
)

// mcpClient drives runMCP over pipes, as Claude Code drives `squad mcp`.
type mcpClient struct {
	t    *testing.T
	in   *io.PipeWriter
	done chan error

	mu      sync.Mutex
	replies map[string]json.RawMessage
	next    int
	closed  sync.Once
}

func startMCP(t *testing.T, env map[string]string) *mcpClient {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &mcpClient{t: t, in: inW, done: make(chan error, 1), replies: map[string]json.RawMessage{}}
	getenv := func(k string) string { return env[k] }
	go func() {
		c.done <- runMCP("squad", "test", inR, outW, getenv)
		outW.Close()
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 64<<10), 16<<20)
		for sc.Scan() {
			var m struct {
				ID json.RawMessage `json:"id"`
			}
			_ = json.Unmarshal(sc.Bytes(), &m)
			c.mu.Lock()
			c.replies[string(m.ID)] = append(json.RawMessage{}, sc.Bytes()...)
			c.mu.Unlock()
		}
	}()
	t.Cleanup(c.close)
	return c
}

func (c *mcpClient) close() {
	c.closed.Do(func() {
		c.in.Close()
		select {
		case <-c.done:
		case <-time.After(10 * time.Second):
			c.t.Error("squad mcp did not end when its stdin closed")
		}
	})
}

func (c *mcpClient) send(v any) {
	b, _ := json.Marshal(v)
	if _, err := c.in.Write(append(b, '\n')); err != nil {
		c.t.Fatal(err)
	}
}

// request sends method with params and waits for its reply.
func (c *mcpClient) request(method string, params any) map[string]any {
	c.t.Helper()
	c.mu.Lock()
	c.next++
	id := c.next
	c.mu.Unlock()
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return c.wait(fmt.Sprint(id))
}

func (c *mcpClient) wait(id string) map[string]any {
	c.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		raw, ok := c.replies[id]
		c.mu.Unlock()
		if ok {
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				c.t.Fatal(err)
			}
			return m
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.t.Fatalf("no reply to %s", id)
	return nil
}

// tool calls a tool and returns its text and whether it is an error.
func (c *mcpClient) tool(name string, args map[string]any) (string, bool) {
	c.t.Helper()
	r := c.request("tools/call", map[string]any{"name": name, "arguments": args})
	res, _ := r["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		c.t.Fatalf("%s: no content in %v", name, r)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

func TestMCPProtocol(t *testing.T) {
	daemon(t)
	c := startMCP(t, map[string]string{"CLAUDE_CODE_SESSION_ID": "s-1", "CLAUDE_PROJECT_DIR": t.TempDir()})

	init := c.request("initialize", map[string]any{"protocolVersion": "2025-03-26"})
	res := init["result"].(map[string]any)
	if res["protocolVersion"] != "2025-03-26" || !strings.Contains(res["instructions"].(string), "other coding agents") {
		t.Fatalf("initialize: %v", init)
	}
	if c.request("initialize", map[string]any{})["result"].(map[string]any)["protocolVersion"] == "" {
		t.Fatal("no default protocol version")
	}
	if _, ok := c.request("ping", nil)["result"]; !ok {
		t.Fatal("ping")
	}
	tools := c.request("tools/list", nil)["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, tl := range tools {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "send,inbox,who,agent" {
		t.Fatalf("tools: %v", names)
	}
	if e, ok := c.request("resources/list", nil)["error"].(map[string]any); !ok || e["code"].(float64) != -32601 {
		t.Fatal("unknown method not refused")
	}
	if e, ok := c.request("tools/call", "not an object")["error"].(map[string]any); !ok || e["code"].(float64) != -32602 {
		t.Fatal("bad params not refused")
	}
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}) // no reply
	c.send("not json at all")

	text, isErr := c.tool("who", nil)
	if isErr || !strings.Contains(text, "solo session") {
		t.Fatalf("who: %s", text)
	}
	if _, isErr := c.tool("fly", nil); !isErr {
		t.Fatal("unknown tool")
	}
	if text, isErr := c.tool("send", map[string]any{"to": "nobody", "body": "x"}); !isErr || !strings.Contains(text, "refused (visibility.unknown_target)") {
		t.Fatalf("refusal: %s", text)
	}

	// Mail from another session shows as a note on the next tool result, then in inbox.
	other := session(t, t.TempDir())
	var me struct{ Name string }
	text, _ = c.tool("agent", map[string]any{"action": "found", "template": "p2p"})
	if !strings.Contains(text, "founded") {
		t.Fatal(text)
	}
	if err := call(context.Background(), proto.VerbWhoami, other, nil, &me); err != nil {
		t.Fatal(err)
	}
	team := strings.Fields(strings.TrimPrefix(text, "Team "))[0]
	var sent string
	if err := call(context.Background(), proto.VerbSend, other, core.SendArgs{To: team, Body: "hello gate"}, &sent); err != nil {
		t.Fatal(err)
	}
	text, _ = c.tool("who", nil)
	if !strings.Contains(text, "[squad] 1 unread message(s)") {
		t.Fatalf("no unread note: %s", text)
	}
	text, _ = c.tool("inbox", map[string]any{"wait_seconds": 0})
	if !strings.Contains(text, "hello gate") || !strings.Contains(text, `sender="`+me.Name+`"`) {
		t.Fatalf("inbox: %s", text)
	}

	// A waiting inbox is cancelled by the client.
	c.mu.Lock()
	c.next++
	id := c.next
	c.mu.Unlock()
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": "inbox", "arguments": map[string]any{"wait_seconds": 60}}})
	time.Sleep(200 * time.Millisecond)
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": id}})
	r := c.wait(fmt.Sprint(id))
	if r["result"].(map[string]any)["isError"] != true {
		t.Fatalf("cancelled inbox: %v", r)
	}
}

func TestSessionArgs(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if a := sessionArgs("/p", env(map[string]string{"CLAUDE_CODE_SESSION_ID": "id"})); a.Harness != "claude" || a.Key != "id" || a.Cwd != "/p" {
		t.Fatalf("%+v", a)
	}
	if a := sessionArgs("/p", env(map[string]string{"CLAUDECODE": "1"})); a.Harness != "claude" || a.Key != "" {
		t.Fatalf("%+v", a)
	}
	if a := sessionArgs("/p", env(nil)); a.Harness != "" || a.Key != "" {
		t.Fatalf("%+v", a)
	}
}

// ensureDaemon starts a real squad binary detached when no daemon answers, and squad mcp
// starts it for a session the same way.
func TestEnsureDaemonStarts(t *testing.T) {
	home(t)
	bin, err := os.Executable() // this test binary: TestMain runs `serve` as squad does
	must(t, err)
	var info struct{ Pid int }
	t.Cleanup(func() {
		_ = adminCall(proto.VerbShutdown, nil, nil)
		for end := time.Now().Add(10 * time.Second); local.Alive(info.Pid) && time.Now().Before(end); {
			time.Sleep(20 * time.Millisecond)
		}
	})
	if err := ensureDaemon(bin); err != nil {
		t.Fatal(err)
	}
	if err := adminCall(proto.VerbView, nil, &struct{}{}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(os.Getenv("SQUAD_HOME"), "daemon.json"))
	_ = json.Unmarshal(b, &info)
	if info.Pid == 0 || info.Pid == os.Getpid() {
		t.Fatalf("daemon pid %d", info.Pid)
	}
}
