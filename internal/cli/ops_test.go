package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bacnx/squad/internal/core"
	"github.com/bacnx/squad/internal/driver/local"
	"github.com/bacnx/squad/internal/proto"
	"github.com/bacnx/squad/internal/server"
	"github.com/bacnx/squad/internal/store"
)

func TestGCAndArchiveCommands(t *testing.T) {
	dir := daemon(t)
	if code, out, _ := squad(t, "gc", "--dry-run"); code != 0 || !strings.Contains(out, "nothing to collect") {
		t.Fatalf("gc: %d %s", code, out)
	}
	cwd := t.TempDir()
	token := session(t, cwd)
	var me struct{ ID, Name string }
	if err := call(context.Background(), proto.VerbWhoami, token, nil, &me); err != nil {
		t.Fatal(err)
	}
	other := session(t, cwd)
	must(t, call(context.Background(), proto.VerbSend, other, core.SendArgs{To: me.Name, Body: "a note"}, nil))
	db, err := store.Open(core.DBPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE participants SET status=?, last_active='2000-01-01T00:00:00Z' WHERE id=?`, core.StatusGone, me.ID)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	code, out, _ := squad(t, "gc", "--closed-before", "1h", "--dry-run")
	if code != 0 || !strings.Contains(out, "solo  "+me.Name) || !strings.Contains(out, "(dry run)") {
		t.Fatalf("gc dry: %d %s", code, out)
	}
	code, out, _ = squad(t, "gc", "--closed-before", "1h")
	if code != 0 || !strings.Contains(out, "mail 1, participants 1") || !strings.Contains(out, "squad archive show") {
		t.Fatalf("gc: %d %s", code, out)
	}
	if code, _, errOut := squad(t, "gc", "--closed-before", "soon"); code != 1 || !strings.Contains(errOut, "want 14d") {
		t.Fatalf("gc soon: %d %s", code, errOut)
	}
	if code, out, _ := squad(t, "gc", "--json"); code != 0 || !strings.Contains(out, `"units": []`) {
		t.Fatalf("gc json: %d %s", code, out)
	}

	_, list, _ := squad(t, "archive", "list")
	file := strings.TrimSpace(list)
	if !strings.Contains(file, "-solo-"+me.Name+"-p-") || !strings.HasSuffix(file, ".json") {
		t.Fatalf("archive list: %q", list)
	}
	code, out, _ = squad(t, "archive", "show", file)
	if code != 0 || !strings.Contains(out, "solo "+me.Name) || !strings.Contains(out, " -> "+me.Name+": a note") {
		t.Fatalf("archive show: %d %s", code, out)
	}
	if code, out, _ := squad(t, "archive", "show", filepath.Join(server.ArchiveDir(dir), file), "--json"); code != 0 || !strings.Contains(out, `"tables"`) {
		t.Fatalf("archive json: %d %s", code, out)
	}
	if code, _, _ := squad(t, "archive", "show", "nope.json"); code != 1 {
		t.Fatal("missing archive shown")
	}
	if code, _, _ := squad(t, "archive"); code != 1 {
		t.Fatal("archive without a verb")
	}
	if code, _, _ := squad(t, "gc", "extra"); code != 1 {
		t.Fatal("gc with an argument")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestModelCommand(t *testing.T) {
	daemon(t)
	token := session(t, t.TempDir())
	agent(t, token, map[string]string{"action": "found", "template": "lead-peer"})
	agent(t, token, map[string]string{"action": "spawn", "role": "peer", "name": "w1", "task": "t"})
	code, out, _ := squad(t, "model", "w1", "sonnet", "--thinking", "high")
	if code != 0 || !strings.Contains(out, "w1: model sonnet, thinking high") {
		t.Fatalf("model: %d %s", code, out)
	}
	if code, _, errOut := squad(t, "model", "w1", "bad"); code != 1 || !strings.Contains(errOut, "nothing stored") {
		t.Fatalf("model bad: %d %s", code, errOut)
	}
	if code, _, _ := squad(t, "model", "w1"); code != 1 {
		t.Fatal("model without a model or thinking")
	}
}

func TestCheckCommand(t *testing.T) {
	dir := home(t)
	code, out, _ := squad(t, "check") // nothing written yet: the defaults, written at the daemon's start
	if code != 0 || !strings.Contains(out, "0 errors, 0 warnings") || !strings.Contains(out, "no config.yaml yet") {
		t.Fatalf("check: %d %s", code, out)
	}
	if err := server.EnsureConfig(dir); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := squad(t, "check"); code != 0 || !strings.Contains(out, "0 errors, 0 warnings") {
		t.Fatalf("check: %d %s", code, out)
	}
	_ = os.WriteFile(server.ConfigPath(dir), []byte(`harness: codex
spawn:
  allowed_roots: [`+"'"+filepath.Join(dir, "none")+"'"+`]
prompts:
  - file: gone.md
    roles: [nosuch/lead]
  - file: rules/general-policy.md
`), 0o600)
	prof, _ := json.Marshal(map[string]any{"cmd": fakeClaude, "thinking": "turbo"})
	_ = os.WriteFile(local.ClaudeProfilePath(dir), prof, 0o600)
	odd := filepath.Join(core.TemplatesDir(dir), "odd")
	_ = os.MkdirAll(odd, 0o700)
	_ = os.WriteFile(filepath.Join(odd, "manifest.yaml"), []byte(`template: odd
summary: roles squad would not start
auto_join_role: lead
roles:
  lead: {description: d, instructions: i, tools: [send, inbox, who, agent], can_spawn: [w, x]}
  w: {description: d, instructions: i, tools: [send, inbox], spawn: {thinking: turbo}}
  x: {description: d, instructions: i, tools: [send, inbox], spawn: {harness: codex}}
routing:
  - {from: lead, to: w}
limits: {depth: 2, concurrency: 2}
`), 0o600)
	bad := filepath.Join(core.TemplatesDir(dir), "broken")
	_ = os.MkdirAll(bad, 0o700)
	_ = os.WriteFile(filepath.Join(bad, "manifest.yaml"), []byte("roles: {}\n"), 0o600)
	code, out, errOut := squad(t, "check")
	for _, s := range []string{`harness "codex"`, "template broken", "is not a directory", "gone.md cannot be read",
		`no template "nosuch"`, "names no roles", `Claude profile: thinking "turbo"`, "template odd: role w: thinking", `template odd: role x: harness "codex"`, "5 errors, 4 warnings"} {
		want(t, out, s)
	}
	if code != 1 || errOut != "" {
		t.Fatalf("check: %d %q", code, errOut)
	}
	_ = os.WriteFile(server.ConfigPath(dir), []byte("colour: red\n"), 0o600)
	if _, out, _ := squad(t, "check"); !strings.Contains(out, "colour") {
		t.Fatal(out)
	}
	if code, _, _ := squad(t, "check", "x"); code != 1 {
		t.Fatal("check with an argument")
	}
	// restart reads config.yaml first: a bad one stops it before the daemon does.
	if code, _, errOut := squad(t, "restart"); code != 1 || !strings.Contains(errOut, "not restarted") {
		t.Fatalf("restart: %d %s", code, errOut)
	}
}

func TestRestartCommand(t *testing.T) {
	daemon(t)
	oldInfo, _ := daemonInfo()
	started := 0
	old := startDaemon
	startDaemon = func() error {
		started++
		go func() { _ = server.Serve("test", "squad") }()
		return up()
	}
	t.Cleanup(func() { startDaemon = old })
	code, out, _ := squad(t, "restart")
	now, _ := daemonInfo()
	if code != 0 || !strings.Contains(out, "squad daemon restarted") || now.Token == oldInfo.Token {
		t.Fatalf("restart: %d %s (token %s -> %s)", code, out, oldInfo.Token, now.Token)
	}
	// Down: it only starts it.
	must(t, adminCall(proto.VerbShutdown, nil, nil))
	waitFor(func() bool { _, err := daemonInfo(); return err != nil })
	code, out, _ = squad(t, "restart")
	if code != 0 || !strings.Contains(out, "was not running") || started != 2 {
		t.Fatalf("restart when down: %d %s", code, out)
	}
	if code, _, _ := squad(t, "restart", "x"); code != 1 {
		t.Fatal("restart with an argument")
	}
}

func TestSetupRemove(t *testing.T) {
	dir := home(t)
	t.Setenv("PATH", fakeBin)
	if code, _, _ := squad(t, "setup", "claude"); code != 0 {
		t.Fatal("setup claude")
	}
	code, out, _ := squad(t, "setup", "remove", "claude")
	if code != 0 || !strings.Contains(out, "MCP server squad, plugin squad@squad, marketplace") {
		t.Fatalf("remove claude: %d %s", code, out)
	}
	if _, err := os.Stat(claudePluginRoot(dir)); !os.IsNotExist(err) {
		t.Fatal("plugin files kept")
	}
	if code, _, _ := squad(t, "setup", "paseo"); code != 0 {
		t.Fatal("setup paseo")
	}
	code, out, _ = squad(t, "setup", "remove", "paseo")
	if code != 0 || !strings.Contains(out, "squad plugin removed from Paseo") {
		t.Fatalf("remove paseo: %d %s", code, out)
	}
	if _, err := os.Stat(paseoDir(dir)); !os.IsNotExist(err) {
		t.Fatal("paseo files kept")
	}
	if code, _, _ := squad(t, "setup", "remove", "codex"); code != 1 {
		t.Fatal("remove codex")
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if code, _, errOut := squad(t, "setup", "remove", "claude"); code != 1 || !strings.Contains(errOut, "not found") {
		t.Fatalf("remove claude: %d %s", code, errOut)
	}
	if code, _, errOut := squad(t, "setup", "remove", "paseo"); code != 1 || !strings.Contains(errOut, "not found") {
		t.Fatalf("remove paseo: %d %s", code, errOut)
	}
}

// A client (squad mcp, the Paseo plugin) may start a daemon of its own while restart waits for
// the old one to go: that daemon is the restarted one.
func TestRestartRacedByAClient(t *testing.T) {
	daemon(t)
	old, _ := daemonInfo()
	saved := startDaemon
	startDaemon = up // only wait: the client below starts it
	t.Cleanup(func() { startDaemon = saved })
	// The client ends with the test, before SQUAD_HOME names the next test's home.
	stop, ended := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		for { // a daemon still starting misses a shutdown: ask until the client is gone
			_ = adminCall(proto.VerbShutdown, nil, nil)
			select {
			case <-ended:
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	})
	go func() { // a client starting the daemon as soon as the old one lets go of its lock
		defer close(ended)
		for range 1000 {
			select {
			case <-stop:
				return
			default:
			}
			start := time.Now()
			_ = server.Serve("client", "squad")
			if time.Since(start) > time.Second {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	code, out, _ := squad(t, "restart")
	now, _ := daemonInfo()
	if code != 0 || !strings.Contains(out, "squad daemon restarted") || now.Token == old.Token || now.Version != "client" {
		t.Fatalf("restart: %d %s %+v", code, out, now)
	}
}
