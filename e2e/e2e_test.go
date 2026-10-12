// Package e2e runs squad as a person does: the built binary's daemon, its CLI, and agents that are
// the fake claude (internal/core/testdata/fakeclaude) talking to `squad mcp`. An agent's work is
// the @directives in the mail it gets, so a test scripts every member of a team.
package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bacnx/squad/internal/driver"
	"github.com/bacnx/squad/internal/driver/local"
)

var squadBin, fakeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "squad-e2e")
	if err != nil {
		panic(err)
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	squadBin, fakeBin = filepath.Join(dir, "squad"+exe), filepath.Join(dir, "claude"+exe)
	// SQUAD_E2E_BIN names a directory with squad and claude (the fake) built already: a test
	// binary built for another system (GOOS=linux go test -c) runs where there is no Go.
	if bin := os.Getenv("SQUAD_E2E_BIN"); bin != "" {
		squadBin, fakeBin = filepath.Join(bin, "squad"+exe), filepath.Join(bin, "claude"+exe)
	} else {
		// With GOCOVERDIR set, squad is built to write its coverage there (go tool covdata reads it).
		cover := []string{}
		if os.Getenv("GOCOVERDIR") != "" {
			cover = []string{"-cover", "-coverpkg=github.com/bacnx/squad/..."}
		}
		for _, b := range [][]string{append(append([]string{"-o", squadBin}, cover...), "../cmd/squad"),
			{"-o", fakeBin, "../internal/core/testdata/fakeclaude"}} {
			if out, err := exec.Command("go", append([]string{"build"}, b...)...).CombinedOutput(); err != nil {
				panic(string(out))
			}
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// env is one squad home with its daemon.
type env struct {
	t      *testing.T
	home   string
	state  string   // FAKE_STATE: the agents' @once and @await marks, the workers' pid marks
	vars   []string // every process's environment
	daemon *exec.Cmd
	exited chan struct{}
}

func newEnv(t *testing.T, vars ...string) *env {
	t.Helper()
	if testing.Short() {
		t.Skip("e2e")
	}
	e := &env{t: t, home: t.TempDir(), state: t.TempDir()}
	// Nothing of the session running the tests may leak in: a squad mcp would wake it.
	for _, kv := range os.Environ() {
		k := strings.ToUpper(kv[:strings.IndexByte(kv, '=')+1])
		if !strings.HasPrefix(k, "CLAUDE") && !strings.HasPrefix(k, "SQUAD_") && !strings.HasPrefix(k, "PASEO_") {
			e.vars = append(e.vars, kv)
		}
	}
	e.vars = append(append(e.vars, "SQUAD_HOME="+e.home, "FAKE_STATE="+e.state), vars...)
	prof := local.ClaudeProfilePath(e.home)
	must(t, os.MkdirAll(filepath.Dir(prof), 0o700))
	b, _ := json.Marshal(map[string]any{"cmd": fakeBin})
	must(t, os.WriteFile(prof, b, 0o600))
	e.start()
	t.Cleanup(e.stop)
	t.Cleanup(func() { // runs first: what the daemon knew when the test failed
		if t.Failed() {
			out, _ := e.run("ps", "--no-start")
			log, _ := e.run("log", "--no-start", "--limit", "200")
			t.Logf("squad ps:\n%s\nsquad log:\n%s", out, log)
			for _, m := range e.workers() {
				tail, _ := e.run("tail", m, "-n", "60", "--no-start")
				t.Logf("squad tail %s:\n%s", m, tail)
			}
		}
	})
	return e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// start runs `squad serve` and waits until it answers.
func (e *env) start() {
	e.t.Helper()
	e.daemon = exec.Command(squadBin, "serve")
	e.daemon.Env = e.vars
	must(e.t, e.daemon.Start())
	e.exited = make(chan struct{})
	go func(c *exec.Cmd, done chan struct{}) { _ = c.Wait(); close(done) }(e.daemon, e.exited)
	waitFor(e.t, "the daemon", func() bool { _, err := e.run("ps", "--no-start"); return err == nil })
}

// crash kills the daemon the way a power cut would: no shutdown.
func (e *env) crash() {
	e.t.Helper()
	must(e.t, e.daemon.Process.Kill())
	<-e.exited
}

func (e *env) stop() {
	_, _ = e.run("stop", "--no-start")
	select {
	case <-e.exited:
	case <-time.After(15 * time.Second):
		_ = e.daemon.Process.Kill()
		<-e.exited
	}
}

// run runs the CLI; err carries its output when it exits non-zero.
func (e *env) run(args ...string) (string, error) {
	cmd := exec.Command(squadBin, args...)
	cmd.Env = e.vars
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("squad %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

// squad runs the CLI and fails the test when it exits non-zero.
func (e *env) squad(args ...string) string {
	e.t.Helper()
	out, err := e.run(args...)
	must(e.t, err)
	return out
}

// ps is what `squad ps --json` shows that the tests look at.
type ps struct {
	Teams []struct {
		Name, Status, Gate string
		Members            []member
	}
	Solos []member
}

type member struct {
	Name, Role, Kind, Status string
	Unread                   int
}

func (e *env) ps() ps {
	e.t.Helper()
	var v ps
	must(e.t, json.Unmarshal([]byte(e.squad("ps", "--json", "--no-start")), &v))
	return v
}

// psQuiet is ps for a cleanup: what it can read, never failing the test.
func (e *env) psQuiet() ps {
	var v ps
	out, _ := e.run("ps", "--json", "--no-start")
	_ = json.Unmarshal([]byte(out), &v)
	return v
}

// member is the named participant ("" Status when there is none).
func (e *env) member(name string) member {
	e.t.Helper()
	v := e.ps()
	for _, t := range v.Teams {
		v.Solos = append(v.Solos, t.Members...)
	}
	for _, m := range v.Solos {
		if m.Name == name {
			return m
		}
	}
	return member{}
}

func (e *env) workers() []string {
	var names []string
	for _, t := range e.psQuiet().Teams {
		for _, m := range t.Members {
			if m.Kind == "worker" {
				names = append(names, m.Name)
			}
		}
	}
	return names
}

func (e *env) waitStatus(name string, status ...string) {
	e.t.Helper()
	waitFor(e.t, name+" "+strings.Join(status, "|"), func() bool { return slices.Contains(status, e.member(name).Status) })
}

// release lets the agents waiting on @await key go on.
func (e *env) release(key string) {
	e.t.Helper()
	must(e.t, os.WriteFile(filepath.Join(e.state, key), nil, 0o600))
}

// call is one squad tool call and its result.
type call struct {
	Tool, Input, Text string
	Err, Done         bool
	id                string
}

// logLine is the part of a stream-json line the tests read: tool calls and their results.
type logLine struct {
	Message struct {
		Content []struct {
			Type, ID, Name, Content string
			Input                   json.RawMessage
			ToolUseID               string `json:"tool_use_id"`
			IsError                 bool   `json:"is_error"`
		}
	}
}

// add appends the line's tool calls to calls and fills in its results. A resumed worker numbers
// its calls afresh, so a result belongs to the last call of its id.
func (l logLine) add(calls []call) []call {
	for _, c := range l.Message.Content {
		switch c.Type {
		case "tool_use":
			calls = append(calls, call{Tool: strings.TrimPrefix(c.Name, "mcp__squad__"), Input: string(c.Input), id: c.ID})
		case "tool_result":
			for i := len(calls) - 1; i >= 0; i-- {
				if calls[i].id == c.ToolUseID {
					calls[i].Text, calls[i].Err, calls[i].Done = c.Content, c.IsError, true
					break
				}
			}
		}
	}
	return calls
}

// toolCalls are the squad tool calls in a worker's log, in order.
func (e *env) toolCalls(name string) []call {
	var calls []call
	out, _ := e.run("tail", name, "-n", "100000", "--json", "--no-start") // none yet: not spawned yet
	for _, line := range strings.Split(out, "\n") {
		var l logLine
		if json.Unmarshal([]byte(line), &l) == nil {
			calls = l.add(calls)
		}
	}
	return calls
}

// patience is how long a test waits for what the daemon or an agent does.
var patience = 20 * time.Second

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(patience); !ok(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// do is the directive calling squad tool verb with args.
func do(verb string, args map[string]any) string {
	b, _ := json.Marshal(args)
	return "@" + verb + " " + string(b)
}

// send is the directive sending body (its lines joined) to to.
func send(to string, body ...string) string {
	return do("send", map[string]any{"to": to, "body": lines(body...)})
}

// spawn is the directive spawning a worker of role named name, task its brief.
func spawn(role, name string, task ...string) string {
	if len(task) == 0 {
		task = []string{"brief"}
	}
	return do("agent", map[string]any{"action": "spawn", "role": role, "name": name, "task": lines(task...)})
}

// once is directive, done only the first time any run sees key.
func once(key, directive string) string { return "@once " + key + " " + directive }

// lines joins directives into one message.
func lines(ds ...string) string { return strings.Join(ds, "\n") }

// agent is a session a person opened: the fake claude with squad as its MCP server, its turns
// given by the test.
type agent struct {
	t     *testing.T
	name  string
	id    string // Claude's session id
	in    io.WriteCloser
	lines chan string
	quit  func()   // the person closes it
	hooks []string // the last turn's hooks: "<Event> <what squad printed>"
}

// session opens a session in dir and makes it join squad.
func (e *env) session(dir string) *agent { return e.open(dir, driver.NewUUID(), false) }

// founder opens a session in dir that founds a team of template.
func (e *env) founder(dir, template string) *agent {
	e.t.Helper()
	a := e.session(dir)
	a.ok(do("agent", map[string]any{"action": "found", "template": template}))
	return a
}

// open opens Claude session id in dir (again, when id was open before); hooked, it has squad's
// hooks plugin: its mail comes with its turns, and squad wakes it when it is idle.
func (e *env) open(dir, id string, hooked bool) *agent {
	e.t.Helper()
	cfg := filepath.Join(e.t.TempDir(), "mcp.json")
	b, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"squad": map[string]any{"type": "stdio", "command": squadBin, "args": []string{"mcp"}}}})
	must(e.t, os.WriteFile(cfg, b, 0o600))
	cmd := exec.Command(fakeBin, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--mcp-config", cfg)
	cmd.Dir = dir
	cmd.Env = append(e.vars, "CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID="+id, "CLAUDE_PROJECT_DIR="+dir)
	if hooked {
		cmd.Env = append(cmd.Env, "FAKE_HOOKS=1")
	}
	in, err := cmd.StdinPipe()
	must(e.t, err)
	stdout, err := cmd.StdoutPipe()
	must(e.t, err)
	must(e.t, cmd.Start())
	// Unbounded enough that a session squad wakes never blocks on its stdout.
	a := &agent{t: e.t, id: id, in: in, lines: make(chan string, 1<<16)}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64<<10), 16<<20)
		for sc.Scan() {
			a.lines <- sc.Text()
		}
		close(a.lines)
	}()
	var once sync.Once
	a.quit = func() { once.Do(func() { in.Close(); _ = cmd.Wait() }) }
	e.t.Cleanup(a.quit)
	who := a.turn(do("who", nil)) // its squad mcp registers it
	if len(who) == 0 {
		e.t.Fatal("no who")
	}
	a.name = strings.TrimRight(strings.Fields(strings.SplitN(who[0].Text, "You are ", 2)[1])[0], ",.:;")
	return a
}

// turn gives the session one message and returns the tool calls of that turn.
func (a *agent) turn(text string) []call {
	a.t.Helper()
	return a.until(a.start(text), text)
}

// start gives the session one message and returns its turn's id.
func (a *agent) start(text string) string {
	a.t.Helper()
	for len(a.lines) > 0 { // what came before (a turn squad woke it for) is not this turn's
		<-a.lines
	}
	id := driver.NewUUID()
	b, _ := json.Marshal(map[string]any{"type": "user", "uuid": id, "message": map[string]any{"role": "user", "content": text}})
	_, err := a.in.Write(append(b, '\n'))
	must(a.t, err)
	return id
}

// woken waits for a turn squad started (a wake) to end, and returns its tool calls.
func (a *agent) woken() []call {
	a.t.Helper()
	return a.until("", "a wake")
}

// until reads the session's output to the end of turn id ("": a turn squad woke it for).
func (a *agent) until(id, what string) []call {
	a.t.Helper()
	var calls []call
	a.hooks = nil
	timeout := time.After(time.Minute)
	for {
		select {
		case line, ok := <-a.lines:
			if !ok {
				a.t.Fatal("session exited")
			}
			var m struct {
				logLine
				Type, State, Subtype string
				Command              string `json:"command_uuid"`
				Hook                 string `json:"hook_event_name"`
				Output               json.RawMessage
			}
			if json.Unmarshal([]byte(line), &m) != nil {
				continue
			}
			if m.Subtype == "hook" {
				a.hooks = append(a.hooks, m.Hook+" "+string(m.Output))
			}
			calls = m.add(calls)
			if m.Type == "command_lifecycle" && m.State != "started" && (m.Command == id || id == "" && strings.HasPrefix(m.Command, "wake-")) {
				return calls
			}
		case <-timeout:
			a.t.Fatalf("turn did not end: %s", what)
		}
	}
}

// ok runs one directive that must succeed and returns its result.
func (a *agent) ok(directive string) string {
	a.t.Helper()
	c := a.turn(directive)
	if len(c) != 1 || c[0].Err {
		a.t.Fatalf("%s: %+v", directive, c)
	}
	return c[0].Text
}

// refused runs one directive that must be refused and returns why.
func (a *agent) refused(directive string) string {
	a.t.Helper()
	c := a.turn(directive)
	if len(c) != 1 || !c[0].Err {
		a.t.Fatalf("%s was not refused: %+v", directive, c)
	}
	return c[0].Text
}

// mail reads the session's inbox until a message containing each of want came, and returns all
// it read.
func (a *agent) mail(want ...string) string {
	a.t.Helper()
	var got strings.Builder
	for end := time.Now().Add(patience); ; {
		got.WriteString(a.ok(do("inbox", map[string]any{"wait_seconds": 2})))
		if !slices.ContainsFunc(want, func(w string) bool { return !strings.Contains(got.String(), w) }) {
			return got.String()
		}
		if time.Now().After(end) {
			a.t.Fatalf("no mail with %q; got:\n%s", want, got.String())
		}
	}
}
