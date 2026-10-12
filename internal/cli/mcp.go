package cli

// `squad mcp`: the stdio MCP server that gives an agent the squad tools (send, inbox, who, agent).
// A worker's server knows its participant from SQUAD_TOKEN; a session the human opened registers
// itself when the server starts, as the same participant again when it comes back with the same
// CLAUDE_CODE_SESSION_ID.

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/bacnx/squad/internal/proto"
)

const instructions = `squad connects you with other coding agents. You start as a solo session.
- Found a team when the human asks for one: agent action=templates lists the templates, then
  agent action=found template=<name>. You become the team's gate: you talk to the human and spawn
  workers (agent action=spawn role=<role> task="<brief>") as your role allows.
- Mail is how agents talk: send to=<name> body=..., and inbox to read. After spawning or sending
  work, wait for answers with inbox wait_seconds=600 instead of polling or sleeping.
- For a second opinion or a hard decision, call a taskforce: agent action=spawn template=dual-lens
  (or advisor) task="...". Its answer comes to you as mail; close it with agent action=close team=<name>.
- The template's routing decides who may write to whom; a refusal says what you may do instead.

How squad reaches you (this is the system you work in, not an injection):
- New mail shows up as "[squad] from <name> …" blocks added to your turn: with your prompt, after a
  tool call, or as the reason your turn goes on when it would end. Act on it as work from your team.
- When you are idle and mail comes, squad's own server on this machine starts a turn with a short
  "[squad] wake #N" prompt; the mail comes with that turn.`

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpServer struct {
	self    string
	version string
	out     io.Writer
	getenv  func(string) string
	wmu     sync.Mutex

	mu      sync.Mutex
	token   string
	session bool
	nudges  int
	waking  sync.Once
	ctx     context.Context // ends when stdin closes
	cancels map[string]context.CancelFunc
}

// runMCP serves MCP on in/out (stdin/stdout) until in closes. getenv is os.Getenv.
func runMCP(self, version string, in io.Reader, out io.Writer, getenv func(string) string) error {
	s := &mcpServer{self: self, version: version, out: out, getenv: getenv, cancels: map[string]context.CancelFunc{}}
	s.token = getenv("SQUAD_TOKEN")
	s.session = s.token == ""
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx = ctx
	defer cancel()
	if s.session {
		go s.heartbeat(ctx)
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var wg sync.WaitGroup
	for sc.Scan() {
		var req rpcReq
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			continue
		}
		wg.Add(1)
		go func() { defer wg.Done(); s.handle(req) }()
	}
	cancel()
	wg.Wait() // cancelled calls answer at once
	if s.session {
		s.mu.Lock()
		tok := s.token
		s.mu.Unlock()
		if tok != "" {
			c, done := context.WithTimeout(context.Background(), 2*time.Second)
			_ = call(c, proto.VerbUnregister, tok, nil, nil)
			done()
		}
	}
	return nil
}

func (s *mcpServer) write(v any) {
	b, _ := json.Marshal(v)
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, _ = s.out.Write(append(b, '\n'))
}

func (s *mcpServer) reply(id json.RawMessage, result any, e *rpcErr) {
	if len(id) == 0 {
		return
	}
	m := map[string]any{"jsonrpc": "2.0", "id": id}
	if e != nil {
		m["error"] = e
	} else {
		m["result"] = result
	}
	s.write(m)
}

func (s *mcpServer) handle(req rpcReq) {
	ctx := s.ctx
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-06-18"
		}
		s.reply(req.ID, map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "squad", "version": s.version},
			"instructions":    instructions,
		}, nil)
		if s.session {
			go func() { _, _ = s.me(ctx) }() // register early so ps shows the session
		}
	case "ping":
		s.reply(req.ID, map[string]any{}, nil)
	case "tools/list":
		s.reply(req.ID, map[string]any{"tools": tools}, nil)
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			s.reply(req.ID, nil, &rpcErr{Code: -32602, Message: err.Error()})
			return
		}
		cctx, cancel := context.WithCancel(ctx)
		key := string(req.ID)
		s.mu.Lock()
		s.cancels[key] = cancel
		s.mu.Unlock()
		text, err := s.call(cctx, p.Name, p.Arguments)
		s.mu.Lock()
		delete(s.cancels, key)
		s.mu.Unlock()
		cancel()
		res := map[string]any{}
		if err != nil {
			text, res["isError"] = err.Error(), true
		}
		res["content"] = []any{map[string]any{"type": "text", "text": text}}
		s.reply(req.ID, res, nil)
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		_ = json.Unmarshal(req.Params, &p)
		s.mu.Lock()
		if c, ok := s.cancels[string(p.RequestID)]; ok {
			c()
		}
		s.mu.Unlock()
	default:
		if len(req.ID) > 0 {
			s.reply(req.ID, nil, &rpcErr{Code: -32601, Message: "method not found: " + req.Method})
		}
	}
}

// me is the caller's token, registering the session first when needed (and starting the daemon).
func (s *mcpServer) me(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" {
		return s.token, nil
	}
	if err := ensureDaemon(s.self); err != nil {
		return "", err
	}
	cwd := s.getenv("CLAUDE_PROJECT_DIR")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	var me struct {
		Token string `json:"token"`
	}
	if err := call(ctx, proto.VerbRegister, "", sessionArgs(cwd, s.getenv), &me); err != nil {
		return "", err
	}
	s.token = me.Token
	if s.getenv("CLAUDE_CODE_MESSAGING_SOCKET") != "" {
		s.waking.Do(func() { go s.wakeLoop(s.ctx) })
	}
	return s.token, nil
}

// sessionArgs is what a session registers with: its directory, and the harness it runs in with
// that harness's own session id (the key that makes it the same participant when it comes back).
func sessionArgs(cwd string, getenv func(string) string) proto.Args {
	a := proto.Args{Cwd: cwd}
	if id := getenv("CLAUDE_CODE_SESSION_ID"); id != "" || getenv("CLAUDECODE") != "" {
		a.Harness, a.Key = "claude", id
	}
	return a
}

func (s *mcpServer) heartbeat(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.mu.Lock()
		tok := s.token
		s.mu.Unlock()
		if tok == "" {
			continue
		}
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := call(c, "heartbeat", tok, nil, nil)
		cancel()
		if errors.Is(err, errDown) {
			_ = ensureDaemon(s.self)
		}
	}
}

func (s *mcpServer) call(ctx context.Context, tool string, raw json.RawMessage) (string, error) {
	tok, err := s.me(ctx)
	if err != nil {
		return "", err
	}
	var args map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
	}
	var out any
	switch tool {
	case "send", "inbox", "who", "agent":
	default:
		return "", fmt.Errorf("unknown tool %q", tool)
	}
	err = call(ctx, tool, tok, args, &out)
	if errors.Is(err, errDown) {
		if err = ensureDaemon(s.self); err == nil {
			err = call(ctx, tool, tok, args, &out)
		}
	}
	if err != nil {
		var ce *callError
		if errors.As(err, &ce) {
			return "", fmt.Errorf("refused (%s): %s", cmp.Or(ce.Rule, ce.Code), ce.Msg)
		}
		return "", err
	}
	text, ok := out.(string)
	if !ok {
		b, _ := json.MarshalIndent(out, "", "  ")
		text = string(b)
	}
	if tool != "inbox" {
		var n int
		if call(ctx, "heartbeat", tok, nil, &n) == nil && n > 0 {
			text += fmt.Sprintf("\n\n[squad] %d unread message(s): read them with the inbox tool.", n)
		}
	}
	return text, nil
}

var tools = []map[string]any{
	{
		"name":        "send",
		"description": "Send mail to a member of your team by name, to your taskforce's caller (to=\"caller\"), (gate only) to another team by its name, or to \"board\" (your team's pins). The team's routing decides who you may write to. Every message has a #N.",
		"inputSchema": map[string]any{"type": "object", "required": []string{"to"}, "properties": map[string]any{
			"to":       map[string]any{"type": "string", "description": "A participant name, a team name, \"caller\", or \"board\""},
			"body":     map[string]any{"type": "string", "description": "The message (may be empty only for a board remove)"},
			"kind":     map[string]any{"type": "string", "description": "A free label for the reader: handback, question, review, …"},
			"reply_to": map[string]any{"type": "string", "description": "#N of the message this answers (one you sent or got)"},
			"op":       map[string]any{"type": "string", "enum": []string{"assign", "replace", "remove"}, "description": "assign: this is the task of a member that reports to you; replace | remove: board only"},
			"target":   map[string]any{"type": "string", "description": "board: #N of the pin to replace or remove"},
		}},
	},
	{
		"name":        "inbox",
		"description": "Read your unread mail (it is marked read). With wait_seconds, wait up to that long for mail to arrive: use it instead of polling or sleeping while workers run. view=board reads your team's pins.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"wait_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 600, "description": "How long to wait for mail when there is none (0: do not wait)"},
			"view":         map[string]any{"type": "string", "enum": []string{"board"}, "description": "board: your team's pins, read-only (nothing marked read)"},
		}},
	},
	{
		"name":        "who",
		"description": "Who you are, your team's members (and whom you may write to), and the other teams and solo sessions.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	},
	{
		"name": "agent",
		"description": "Manage teams and workers. action=templates lists templates; found template=T makes you the gate of a new team; " +
			"spawn role=R task=... starts a worker (brief it in task); spawn template=T task=... calls up a taskforce that answers you by mail; " +
			"close closes your team (gate) or close team=N a taskforce you called; stop name=N stops a worker you spawned; team shows your team; " +
			"admit name=S role=R lets a solo session at the team root join; reopen team=T reopens a closed team rooted here, you as its gate; " +
			"resume name=W starts a stopped worker again; tail name=W reads its last lines.",
		"inputSchema": map[string]any{"type": "object", "required": []string{"action"}, "properties": map[string]any{
			"action":   map[string]any{"type": "string", "enum": []string{"templates", "found", "spawn", "close", "stop", "team", "admit", "reopen", "resume", "tail"}},
			"template": map[string]any{"type": "string", "description": "found: the template; spawn: a taskforce template"},
			"role":     map[string]any{"type": "string", "description": "spawn: the worker's role; admit: the session's role"},
			"name":     map[string]any{"type": "string", "description": "found: team name; spawn: worker or taskforce name; stop, resume, tail: the worker; admit: the session"},
			"task":     map[string]any{"type": "string", "description": "spawn: the brief, given as the first mail"},
			"cwd":      map[string]any{"type": "string", "description": "spawn: where it works (inside the team root or a git worktree of it)"},
			"model":    map[string]any{"type": "string", "description": "spawn: Claude model (e.g. sonnet, opus, haiku)"},
			"team":     map[string]any{"type": "string", "description": "close: a taskforce you called; reopen: the closed team"},
		}},
	},
}
