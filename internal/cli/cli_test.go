package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bacnx/squad/internal/testdata/fake"

	"github.com/bacnx/squad/internal/core"
	"github.com/bacnx/squad/internal/driver/local"
	"github.com/bacnx/squad/internal/proto"
	"github.com/bacnx/squad/internal/server"
)

var (
	fakeClaude string // the fake claude binary
	fakeBin    string // a directory with claude and paseo (both the fake) for PATH
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "serve" { // started as the daemon (TestEnsureDaemonStarts)
		os.Exit(Main(os.Args[1:]))
	}
	var done func()
	fakeClaude, done = fake.Build()
	fakeBin = filepath.Dir(fakeClaude) // the fake as claude, and as paseo
	b, _ := os.ReadFile(fakeClaude)
	if err := os.WriteFile(filepath.Join(fakeBin, "paseo"+filepath.Ext(fakeClaude)), b, 0o700); err != nil {
		panic(err)
	}
	code := m.Run()
	done()
	os.Exit(code)
}

// home gives the test a fresh SQUAD_HOME whose Claude profile runs the fake claude.
func home(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SQUAD_HOME", dir)
	if err := os.MkdirAll(filepath.Dir(local.ClaudeProfilePath(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"cmd": fakeClaude})
	if err := os.WriteFile(local.ClaudeProfilePath(dir), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// daemon runs squad serve in this process over a fresh home until the test ends.
func daemon(t *testing.T) string {
	t.Helper()
	dir := home(t)
	done := make(chan error, 1)
	go func() { done <- server.Serve("test", "squad") }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := call(ctx, proto.VerbPing, "", nil, nil)
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon did not come up: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(func() {
		if adminCall(proto.VerbShutdown, nil, nil) == nil {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
			}
		}
	})
	return dir
}

// squad runs the command line and returns its exit code, stdout and stderr.
func squad(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	ro, wo, _ := os.Pipe()
	re, we, _ := os.Pipe()
	os.Stdout, os.Stderr = wo, we
	outc, errc := make(chan string), make(chan string)
	go func() { b, _ := io.ReadAll(ro); outc <- string(b) }()
	go func() { b, _ := io.ReadAll(re); errc <- string(b) }()
	code := Main(args)
	wo.Close()
	we.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	return code, <-outc, <-errc
}

func want(t *testing.T, got, sub string) {
	t.Helper()
	if !strings.Contains(got, sub) {
		t.Fatalf("output lacks %q:\n%s", sub, got)
	}
}

// session registers a session as `squad mcp` would and returns its token.
func session(t *testing.T, cwd string) string {
	t.Helper()
	var me struct{ Token string }
	if err := call(context.Background(), proto.VerbRegister, "", proto.Args{Cwd: cwd, Harness: "claude"}, &me); err != nil {
		t.Fatal(err)
	}
	return me.Token
}

func agent(t *testing.T, token string, args map[string]string) string {
	t.Helper()
	var out string
	if err := call(context.Background(), proto.VerbAgent, token, args, &out); err != nil {
		t.Fatalf("agent %v: %v", args, err)
	}
	return out
}

func TestParseFlagsAfterPositional(t *testing.T) {
	fs := flag.NewFlagSet("tail", flag.ContinueOnError)
	n := fs.Int("n", 40, "")
	view := fs.Bool("view", false, "")
	noStart := fs.Bool("no-start", false, "")
	pos, err := parse(fs, []string{"w1", "-n", "5", "--view", "--no-start", "--", "-x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 2 || pos[0] != "w1" || pos[1] != "-x" || *n != 5 || !*view || !*noStart {
		t.Fatalf("pos=%v n=%d view=%v noStart=%v", pos, *n, *view, *noStart)
	}
	if _, err := parse(fs, []string{"--bogus"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
}

func TestHelpVersionUnknown(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"--help"}} {
		code, out, _ := squad(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d", args, code)
		}
		want(t, out, "squad setup [claude|paseo]")
	}
	code, out, _ := squad(t, "version")
	if code != 0 || !strings.HasPrefix(out, "squad ") {
		t.Fatalf("version: %d %q", code, out)
	}
	code, _, errOut := squad(t, "fly")
	if code != 1 {
		t.Fatalf("unknown command exit %d", code)
	}
	want(t, errOut, `unknown command "fly"`)
	code, _, errOut = squad(t, "ps", "--bogus")
	if code != 1 || !strings.Contains(errOut, "bogus") {
		t.Fatalf("bad flag: %d %s", code, errOut)
	}
}

// With --no-start and no daemon every command fails with the message the Paseo plugin looks for.
func TestNoStartWhenDown(t *testing.T) {
	home(t)
	for _, args := range [][]string{
		{"ps", "--no-start"}, {"ps", "--view", "--no-start"}, {"top", "--no-start"},
		{"tail", "w1", "-n", "5", "--view", "--no-start"}, {"kill", "w1", "--no-start"},
		{"team", "down", "x", "--no-start"},
	} {
		code, _, errOut := squad(t, args...)
		if code != 1 || !strings.Contains(errOut, proto.DownMessage) {
			t.Fatalf("%v: %d %q", args, code, errOut)
		}
	}
	code, out, _ := squad(t, "stop")
	if code != 0 {
		t.Fatal(code)
	}
	want(t, out, "not running")
}

func TestUsageErrors(t *testing.T) {
	home(t)
	for _, args := range [][]string{
		{"tail"}, {"kill"}, {"x", "a", "b"}, {"team", "up", "x"}, {"template"}, {"template", "new", "x"},
		{"setup", "codex"},
	} {
		if code, _, errOut := squad(t, args...); code != 1 || !strings.Contains(errOut, "usage") {
			t.Fatalf("%v: %d %q", args, code, errOut)
		}
	}
}

func TestPsTailAndWorkerCommands(t *testing.T) {
	daemon(t)
	root := t.TempDir()
	lead := session(t, root)
	solo := session(t, t.TempDir())
	agent(t, lead, map[string]string{"action": "found", "template": "lead-peer", "name": "api"})
	agent(t, lead, map[string]string{"action": "spawn", "role": "peer", "name": "w1", "task": "fix the tests"})
	agent(t, solo, map[string]string{"action": "spawn", "template": "advisor", "name": "adv", "task": "?"})
	agent(t, lead, map[string]string{"action": "spawn", "template": "advisor", "name": "old", "task": "?"})
	agent(t, lead, map[string]string{"action": "close", "team": "old"})

	// The task reaches the worker's log.
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, out, _ := squad(t, "tail", "w1", "--no-start")
		if strings.Contains(out, "fix the tests") && strings.Contains(out, "turn done") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tail never showed the task:\n%s", out)
		}
		time.Sleep(50 * time.Millisecond)
	}

	code, out, _ := squad(t, "ps", "--no-start")
	if code != 0 {
		t.Fatal(code)
	}
	for _, s := range []string{"team api [lead-peer]", "w1", "peer", "taskforce of", "solo sessions", "closed: old", "latest events:"} {
		want(t, out, s)
	}
	topFrames, topEvery = 2, time.Millisecond
	defer func() { topFrames, topEvery = 0, 2*time.Second }()
	code, out, _ = squad(t, "top", "--no-start")
	if code != 0 || strings.Count(out, "(ctrl+c to quit)") != 2 {
		t.Fatalf("top: %d\n%s", code, out)
	}

	for _, flag := range []string{"--json", "--view"} {
		code, out, _ = squad(t, "ps", flag, "--no-start")
		var v struct {
			Version int
			Teams   []struct{ Name, Parent string }
		}
		if code != 0 || json.Unmarshal([]byte(out), &v) != nil || v.Version != 1 || len(v.Teams) != 3 {
			t.Fatalf("ps %s: %d\n%s", flag, code, out)
		}
	}
	code, out, _ = squad(t, "tail", "w1", "-n", "3", "--view", "--no-start")
	var tv struct {
		Version int
		Lines   []struct{ Kind, Text string }
	}
	if code != 0 || json.Unmarshal([]byte(out), &tv) != nil || tv.Version != 1 || len(tv.Lines) == 0 || len(tv.Lines) > 3 {
		t.Fatalf("tail --view: %d\n%s", code, out)
	}
	code, out, _ = squad(t, "tail", "w1", "-n", "2", "--json", "--no-start")
	if code != 0 || strings.Count(strings.TrimSpace(out), "\n") != 1 || !strings.HasPrefix(out, "{") {
		t.Fatalf("tail --json: %d\n%s", code, out)
	}
	if code, _, errOut := squad(t, "tail", "ghost", "--no-start"); code != 1 || !strings.Contains(errOut, "no participant") {
		t.Fatalf("tail ghost: %d %s", code, errOut)
	}

	// tail -f prints what is appended, until stopped.
	followStop = make(chan struct{})
	logPath := core.WorkerLog(server.Dir(), resolve(t, "w1"))
	go func() {
		time.Sleep(200 * time.Millisecond)
		f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
		f.WriteString(`{"type":"assistant","message":{"content":"appended later"}}` + "\n")
		f.Close()
		time.Sleep(700 * time.Millisecond)
		close(followStop)
	}()
	code, out, _ = squad(t, "tail", "w1", "-n", "1", "-f", "--no-start")
	followStop = nil
	if code != 0 {
		t.Fatal(code)
	}
	want(t, out, "appended later")

	code, out, _ = squad(t, "abort", "w1", "--no-start")
	if code != 0 {
		t.Fatal(code)
	}
	want(t, out, "interrupted w1")
	code, out, _ = squad(t, "x", "w1", "--no-start")
	if code != 0 {
		t.Fatal(code)
	}
	want(t, out, "stopping w1")
	deadline = time.Now().Add(10 * time.Second)
	for {
		_, out, _ = squad(t, "resume", "w1", "--no-start")
		if strings.Contains(out, "resumed w1") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resume: %s", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var me struct{ Name string }
	if err := call(context.Background(), proto.VerbWhoami, lead, nil, &me); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := squad(t, "kill", me.Name, "--no-start"); code != 1 || !strings.Contains(errOut, "session") {
		t.Fatalf("kill a session: %d %s", code, errOut)
	}
	code, out, _ = squad(t, "team", "down", "api", "--no-start")
	if code != 0 {
		t.Fatal(code)
	}
	want(t, out, "closed api")

	code, out, _ = squad(t, "stop")
	if code != 0 {
		t.Fatal(code)
	}
	want(t, out, "stopping the squad daemon")
}

func resolve(t *testing.T, name string) string {
	t.Helper()
	var p struct{ ID string }
	if err := adminCall(proto.VerbResolve, proto.Args{Name: name}, &p); err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func TestTemplateCommands(t *testing.T) {
	dir := home(t)
	code, out, _ := squad(t, "template", "list")
	if code != 0 {
		t.Fatal(code)
	}
	for _, s := range []string{"NAME", "slp", "dual-lens", "built-in", "yes"} {
		want(t, out, s)
	}
	code, out, _ = squad(t, "template", "new", "mine", "--from", "slp")
	if code != 0 {
		t.Fatal(code)
	}
	want(t, out, filepath.Join(core.TemplatesDir(dir), "mine", "manifest.yaml"))
	if code, _, errOut := squad(t, "template", "new", "mine", "--from", "slp"); code != 1 || !strings.Contains(errOut, "exists") {
		t.Fatalf("again: %d %s", code, errOut)
	}
	_, out, _ = squad(t, "template", "list")
	want(t, out, "yours")
}

// setup runs only the fake claude and paseo on PATH, and writes only under SQUAD_HOME.
func TestSetup(t *testing.T) {
	dir := home(t)
	t.Setenv("PATH", fakeBin)
	code, out, _ := squad(t, "setup")
	if code != 0 {
		t.Fatal(code)
	}
	for _, s := range []string{"2.1.289 (Claude Code): MCP server squad added, plugin squad@squad enabled", "plugin squad installed", dir} {
		want(t, out, s)
	}
	code, out, _ = squad(t, "setup", "claude")
	if code != 0 {
		t.Fatal(code)
	}
	want(t, out, "squad added to Claude Code")
	want(t, out, "squad@squad")
	hooks, err := os.ReadFile(filepath.Join(claudePluginRoot(dir), "squad", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range claudeSessionHooks {
		if !strings.Contains(string(hooks), "hook claude "+ev) {
			t.Errorf("hooks.json lacks %s", ev)
		}
	}
	if !strings.Contains(string(hooks), `"matcher": "mcp__squad__.*"`) || !strings.Contains(string(hooks), filepath.ToSlash(self())) {
		t.Errorf("hooks.json: %s", hooks)
	}
	for _, f := range []string{".claude-plugin/marketplace.json", "squad/.claude-plugin/plugin.json"} {
		if _, err := os.Stat(filepath.Join(claudePluginRoot(dir), filepath.FromSlash(f))); err != nil {
			t.Error(err)
		}
	}

	code, out, _ = squad(t, "setup", "paseo")
	if code != 0 {
		t.Fatalf("setup paseo: %d %s", code, out)
	}
	want(t, out, "Squad plugin installed in Paseo")
	installed, err := os.ReadFile(filepath.Join(paseoDir(dir), "server", "installed.ts"))
	if err != nil {
		t.Fatal(err)
	}
	q, _ := json.Marshal(self())
	if !bytes.Contains(installed, []byte("export const squadPath = "+string(q)+";")) {
		t.Fatalf("installed.ts: %s", installed)
	}
	if _, err := os.Stat(filepath.Join(paseoDir(dir), "paseo-plugin.json")); err != nil {
		t.Fatal(err)
	}

	// Nothing on PATH, and no Paseo app: both are reported, setup claude and paseo fail.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	_, out, _ = squad(t, "setup")
	want(t, out, "claude  not found")
	want(t, out, "paseo   not found")
	if code, _, errOut := squad(t, "setup", "claude"); code != 1 || !strings.Contains(errOut, "not found") {
		t.Fatalf("setup claude: %d %s", code, errOut)
	}
	if code, _, errOut := squad(t, "setup", "paseo"); code != 1 || !strings.Contains(errOut, "paseo not found") {
		t.Fatalf("setup paseo: %d %s", code, errOut)
	}
}

func TestDiagCommands(t *testing.T) {
	daemon(t)
	lead := session(t, t.TempDir())
	agent(t, lead, map[string]string{"action": "found", "template": "lead-peer", "name": "api"})
	agent(t, lead, map[string]string{"action": "spawn", "role": "peer", "name": "w1", "task": "t"})
	var me struct{ Name string }
	if err := call(context.Background(), proto.VerbWhoami, lead, nil, &me); err != nil {
		t.Fatal(err)
	}
	code, out, _ := squad(t, "why", "w1", me.Name, "--no-start")
	if code != 0 {
		t.Fatal(code)
	}
	for _, s := range []string{"caller", "routing", "allows peer -> lead", "verdict: allow"} {
		want(t, out, s)
	}
	_, out, _ = squad(t, "why", "w1", "notify", "--no-start")
	want(t, out, "verdict: deny rule=routing.notify")
	code, out, _ = squad(t, "why", "w1", me.Name, "--json", "--no-start")
	if code != 0 || !strings.Contains(out, `"verdict": "allow"`) {
		t.Fatalf("why --json: %s", out)
	}
	if code, _, errOut := squad(t, "why", "w1", "--no-start"); code != 1 || !strings.Contains(errOut, "usage") {
		t.Fatal(errOut)
	}
	code, out, _ = squad(t, "log", "--team", "api", "--limit", "1", "--no-start")
	if code != 0 || strings.Count(out, "\n") != 1 || !strings.Contains(out, "founded team api") {
		t.Fatalf("log: %d\n%s", code, out)
	}
	code, out, _ = squad(t, "log", "--json", "--no-start")
	if code != 0 || !strings.HasPrefix(out, "[") {
		t.Fatalf("log --json: %s", out)
	}
	code, out, _ = squad(t, "doctor", "--no-start")
	if code != 0 || !strings.Contains(out, "no findings") {
		t.Fatalf("doctor: %d %s", code, out)
	}
	// Over the rate: held, released by hand; doctor sees the hold until then.
	for i := 0; i < 31; i++ {
		var sent string
		_ = call(context.Background(), proto.VerbSend, lead, map[string]string{"to": "w1", "body": fmt.Sprint(i)}, &sent)
		if strings.Contains(sent, "HELD") {
			n := strings.TrimPrefix(strings.Fields(sent)[1], "#")
			code, out, _ = squad(t, "doctor", "--no-start")
			if code != 1 || !strings.Contains(out, "held") {
				t.Fatalf("doctor with a hold: %d %s", code, out)
			}
			code, out, _ = squad(t, "doctor", "--json", "--no-start")
			if code != 1 || !strings.Contains(out, `"kind": "held"`) {
				t.Fatalf("doctor --json: %s", out)
			}
			code, out, _ = squad(t, "release", "#"+n, "--no-start")
			if code != 0 || !strings.Contains(out, "released #"+n) {
				t.Fatalf("release: %d %s", code, out)
			}
			if code, _, errOut := squad(t, "release", "--no-start"); code != 1 || !strings.Contains(errOut, "usage") {
				t.Fatal(errOut)
			}
			return
		}
	}
	t.Fatal("nothing was held")
}
