package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bacnx/squad/internal/testdata/fake"

	"github.com/bacnx/squad/internal/core"
	"github.com/bacnx/squad/internal/driver/local"
	"github.com/bacnx/squad/internal/proto"
)

var fakeClaude string

func TestMain(m *testing.M) {
	var done func()
	fakeClaude, done = fake.Build()
	code := m.Run()
	done()
	os.Exit(code)
}

// serve runs a daemon in this process over a fresh home and returns its daemon.json, and a
// channel closed when Serve returns.
func serve(t *testing.T) (proto.Info, <-chan error) {
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
	done := make(chan error, 1)
	go func() { done <- Serve("test", "squad") }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, err := os.ReadFile(DaemonFile(dir))
		var info proto.Info
		if err == nil && json.Unmarshal(b, &info) == nil && info.Addr != "" {
			t.Cleanup(func() {
				// A test that stopped the daemon itself leaves nothing to wait for.
				if r, err := rpc(t, info, proto.VerbShutdown, info.Token, nil); err == nil && r.OK {
					select {
					case <-done:
					case <-time.After(10 * time.Second):
					}
				}
			})
			return info, done
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not come up")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func rpc(t *testing.T, info proto.Info, op, token string, args any) (proto.Response, error) {
	t.Helper()
	a, _ := json.Marshal(args)
	body, _ := json.Marshal(proto.Request{Op: op, Token: token, Args: a})
	resp, err := http.Post("http://"+info.Addr+"/rpc", "application/json", bytes.NewReader(body))
	if err != nil {
		return proto.Response{}, err
	}
	defer resp.Body.Close()
	var r proto.Response
	err = json.NewDecoder(resp.Body).Decode(&r)
	return r, err
}

func ok(t *testing.T, info proto.Info, op, token string, args, out any) {
	t.Helper()
	r, err := rpc(t, info, op, token, args)
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	if !r.OK {
		t.Fatalf("%s: %s %s", op, r.Code, r.Error)
	}
	if out != nil {
		if err := json.Unmarshal(r.Result, out); err != nil {
			t.Fatalf("%s result: %v", op, err)
		}
	}
}

func refused(t *testing.T, info proto.Info, op, token string, args any, code string) {
	t.Helper()
	r, err := rpc(t, info, op, token, args)
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	if r.OK || r.Code != code {
		t.Fatalf("%s: want %s, got %+v", op, code, r)
	}
}

func TestServeVerbs(t *testing.T) {
	info, _ := serve(t)
	admin := info.Token
	var pong string
	ok(t, info, proto.VerbPing, admin, nil, &pong)
	if pong != "pong" || info.Version != "test" || info.Pid != os.Getpid() {
		t.Fatalf("ping %q, info %+v", pong, info)
	}

	// Admin verbs need the admin token; participant verbs a participant's.
	refused(t, info, proto.VerbView, "nope", nil, "auth")
	refused(t, info, proto.VerbWho, "nope", nil, "auth")
	refused(t, info, "no_such_verb", "nope", nil, "auth") // closed unless listed
	refused(t, info, "fly", admin, nil, "invalid")

	var me struct{ ID, Name, Token, Team, Role string }
	root := t.TempDir()
	ok(t, info, proto.VerbRegister, admin, proto.Args{Cwd: root, Key: "k1", Harness: "claude"}, &me)
	ok(t, info, proto.VerbWhoami, me.Token, nil, &me)
	var unread int
	ok(t, info, proto.VerbHeartbeat, me.Token, nil, &unread)
	var text string
	ok(t, info, proto.VerbWho, me.Token, nil, &text)
	ok(t, info, proto.VerbAgent, me.Token, map[string]string{"action": "found", "template": "lead-peer"}, &text)
	ok(t, info, proto.VerbAgent, me.Token, map[string]string{"action": "spawn", "role": "peer", "name": "w1", "task": "t"}, &text)
	refused(t, info, proto.VerbAgent, me.Token, map[string]string{"action": "spawn", "role": "lead", "task": "t"}, "permission")
	ok(t, info, proto.VerbSend, me.Token, core.SendArgs{To: "w1", Body: "hi"}, &text)
	refused(t, info, proto.VerbSend, me.Token, proto.Args{To: "w1"}, "invalid")
	ok(t, info, proto.VerbInbox, me.Token, proto.Args{Wait: 0}, &text)
	if text != "no new mail" {
		t.Fatalf("inbox: %q", text)
	}

	var p map[string]string
	ok(t, info, proto.VerbResolve, admin, proto.Args{Name: "w1"}, &p)
	if p["kind"] != "worker" {
		t.Fatalf("resolve: %v", p)
	}
	refused(t, info, proto.VerbResolve, admin, proto.Args{Name: "ghost"}, "not_found")
	ok(t, info, proto.VerbInterrupt, admin, proto.Args{Name: "w1"}, &text)
	// The live fake answers set_model and apply_flag_settings; "bad" it refuses.
	ok(t, info, proto.VerbModel, admin, proto.Args{Name: "w1", Model: "sonnet", Thinking: "high"}, &text)
	if !strings.Contains(text, "model sonnet, thinking high, from its next turn") {
		t.Fatalf("model: %q", text)
	}
	refused(t, info, proto.VerbModel, admin, proto.Args{Name: "w1", Thinking: "bad"}, "invalid")
	refused(t, info, proto.VerbModel, admin, proto.Args{Name: me.Name, Model: "x"}, "invalid")
	var gc struct{ Units []any }
	ok(t, info, proto.VerbGC, admin, proto.Args{Before: "1h", DryRun: true}, &gc)
	if len(gc.Units) != 0 {
		t.Fatalf("gc: %+v", gc)
	}
	ok(t, info, proto.VerbGC, admin, proto.Args{}, &gc)
	refused(t, info, proto.VerbGC, admin, proto.Args{Before: "soon"}, "invalid")
	refused(t, info, proto.VerbGC, admin, proto.Args{Before: "off"}, "invalid")
	ok(t, info, proto.VerbKill, admin, proto.Args{Name: "w1"}, &text)
	var view struct {
		Version int
		Teams   []struct{ Name string }
	}
	ok(t, info, proto.VerbView, admin, nil, &view)
	if view.Version != 1 || len(view.Teams) != 1 {
		t.Fatalf("view: %+v", view)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, _ := rpc(t, info, proto.VerbResume, admin, proto.Args{Name: "w1"})
		if r.OK && strings.Contains(string(r.Result), "resumed") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resume: %+v", r)
		}
		time.Sleep(50 * time.Millisecond)
	}
	ok(t, info, proto.VerbTeamDown, admin, proto.Args{Name: me.Name}, &text)
	ok(t, info, proto.VerbUnregister, me.Token, nil, nil)
	refused(t, info, proto.VerbAgent, me.Token, "not an object", "invalid")
	refused(t, info, proto.VerbSend, me.Token, "not an object", "invalid")
}

func TestServeHTTPErrors(t *testing.T) {
	info, _ := serve(t)
	resp, err := http.Get("http://" + info.Addr + "/rpc")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /rpc: %d", resp.StatusCode)
	}
	resp, err = http.Post("http://"+info.Addr+"/rpc", "application/json", strings.NewReader("{"))
	if err != nil {
		t.Fatal(err)
	}
	var r proto.Response
	_ = json.NewDecoder(resp.Body).Decode(&r)
	resp.Body.Close()
	if r.OK || r.Code != "invalid" {
		t.Fatalf("bad body: %+v", r)
	}
}

// One daemon per home: a second Serve on the same home refuses while the first runs; shutdown
// ends the first, removes daemon.json and frees the lock.
func TestServeLockAndShutdown(t *testing.T) {
	info, done := serve(t)
	if err := Serve("test", "squad"); err == nil || !strings.Contains(err.Error(), "already runs") {
		t.Fatalf("second Serve: %v", err)
	}
	ok(t, info, proto.VerbShutdown, info.Token, nil, nil)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not stop")
	}
	dir := Dir()
	if _, err := os.Stat(DaemonFile(dir)); !os.IsNotExist(err) {
		t.Fatalf("daemon.json left: %v", err)
	}
	unlock, err := lock(dir)
	if err != nil {
		t.Fatalf("lock after shutdown: %v", err)
	}
	unlock()
	// A stale lock (its pid is gone) is taken over.
	if err := os.WriteFile(filepath.Join(dir, "squad.lock"), []byte("999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err = lock(dir)
	if err != nil {
		t.Fatalf("stale lock: %v", err)
	}
	unlock()
}

func TestDir(t *testing.T) {
	t.Setenv("SQUAD_HOME", "")
	home, _ := os.UserHomeDir()
	if Dir() != filepath.Join(home, ".squad") {
		t.Fatal(Dir())
	}
	t.Setenv("SQUAD_HOME", "/x/y")
	if Dir() != "/x/y" || ServeLog("/x") != filepath.Join("/x", "serve.log") {
		t.Fatal(Dir())
	}
}

func TestServeMailVerbs(t *testing.T) {
	info, _ := serve(t)
	admin := info.Token
	var lead, w struct{ Name, Token string }
	ok(t, info, proto.VerbRegister, admin, proto.Args{Cwd: t.TempDir(), Harness: "claude"}, &lead)
	var text string
	ok(t, info, proto.VerbAgent, lead.Token, map[string]string{"action": "found", "template": "lead-peer"}, &text)
	ok(t, info, proto.VerbAgent, lead.Token, map[string]string{"action": "spawn", "role": "peer", "name": "w1", "task": "t"}, &text)
	ok(t, info, proto.VerbSend, lead.Token, map[string]string{"to": "w1", "body": "go", "kind": "brief"}, &text)
	if !strings.HasPrefix(text, "sent #") {
		t.Fatalf("send: %q", text)
	}
	ok(t, info, proto.VerbInbox, lead.Token, proto.Args{View: "board"}, &text)
	if text != "Nothing in this view." {
		t.Fatalf("board: %q", text)
	}
	refused(t, info, proto.VerbInbox, lead.Token, proto.Args{View: "nope"}, "invalid")
	var why struct {
		Checks  []map[string]string
		Verdict string
	}
	ok(t, info, proto.VerbWhy, admin, proto.Args{Name: "w1", To: lead.Name}, &why)
	if why.Verdict != "allow" || len(why.Checks) < 3 {
		t.Fatalf("why: %+v", why)
	}
	refused(t, info, proto.VerbWhy, admin, proto.Args{Name: "ghost", To: lead.Name}, "not_found")
	var evs []map[string]any
	ok(t, info, proto.VerbLog, admin, proto.Args{Limit: 2}, &evs)
	if len(evs) != 2 {
		t.Fatalf("log: %+v", evs)
	}
	var fs []map[string]any
	ok(t, info, proto.VerbDoctor, admin, nil, &fs)
	refused(t, info, proto.VerbRelease, admin, proto.Args{Name: "#1"}, "invalid") // not held
	refused(t, info, proto.VerbSend, lead.Token, map[string]string{"to": "notify", "body": "x"}, "routing")
	_ = w
}
