package manifests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuiltinsLoad(t *testing.T) {
	list, err := List(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"p2p": false, "lead-peer": false, "slp": false, "dual-lens": true, "advisor": true}
	if len(list) != len(want) {
		t.Fatalf("got %d templates: %+v", len(list), list)
	}
	for _, info := range list {
		tf, ok := want[info.Name]
		if !ok || info.Taskforce != tf || info.Origin != "built-in" {
			t.Errorf("unexpected %+v", info)
		}
	}
	m, err := Load(t.TempDir(), "slp")
	if err != nil {
		t.Fatal(err)
	}
	if m.Roles["lead"].Instructions == "" || !m.Allows("peer", "lead") || m.Allows("peer", "supervisor") {
		t.Fatalf("slp loaded wrong: %+v", m.Roles["lead"])
	}
}

func TestCopyBuiltin(t *testing.T) {
	dir := t.TempDir()
	if _, err := CopyBuiltin(dir, "slp", "mine"); err != nil {
		t.Fatal(err)
	}
	m, err := Load(dir, "mine")
	if err != nil {
		t.Fatal(err)
	}
	if m.Template != "mine" || m.Roles["supervisor"].Instructions == "" {
		t.Fatalf("copy: %+v", m)
	}
}

func TestManifestQueries(t *testing.T) {
	m, err := Load(t.TempDir(), "slp")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Targets("lead"); len(got) != 2 || got[0] != "supervisor" || got[1] != "peer" {
		t.Fatalf("Targets(lead) = %v", got)
	}
	if !m.HasTool("lead", "agent") || m.HasTool("peer", "agent") || m.HasTool("nobody", "send") {
		t.Fatal("HasTool")
	}
	if m.SessionRole() != "supervisor" {
		t.Fatal(m.SessionRole())
	}
	one, err := Parse([]byte("template: one\nroles:\n  solo: {tools: [send]}\n"), nil)
	if err != nil || one.SessionRole() != "solo" {
		t.Fatalf("single role: %v %v", one, err)
	}
}

func TestValidateErrors(t *testing.T) {
	read := func(string) ([]byte, error) { return nil, os.ErrNotExist }
	for name, text := range map[string]string{
		"no template":       "roles:\n  a: {}\n",
		"no roles":          "template: x\n",
		"two roles no auto": "template: x\nroles:\n  a: {}\n  b: {}\n",
		"auto not a role":   "template: x\nauto_join_role: z\nroles:\n  a: {}\n  b: {}\n",
		"can_spawn ghost":   "template: x\nroles:\n  a: {can_spawn: [ghost]}\n",
		"routing ghost":     "template: x\nroles:\n  a: {}\nrouting:\n  - {from: a, to: ghost}\n",
		"unknown key":       "template: x\nflavour: mint\nroles:\n  a: {}\n",
		"empty role":        "template: x\nroles:\n  a:\n",
		"missing file":      "template: x\nroles:\n  a: {instructions_file: gone.md}\n",
		"not yaml":          "template: [\n",
		"unknown tool":      "template: x\nauto_join_role: a\nroles:\n  a: {tools: [send, fly]}\n",
	} {
		if _, err := Parse([]byte(text), read); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLoadAndListUserTemplates(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "manifest.yaml"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("mine", "template: mine\nsummary: Mine.\ntaskforce: {idle_for: 5m}\nroles:\n  only: {tools: [send], instructions_file: p.md}\n")
	if err := os.WriteFile(filepath.Join(dir, "mine", "p.md"), []byte("Do it."), 0o600); err != nil {
		t.Fatal(err)
	}
	write("broken", "template: [\n")
	write("slp", "template: slp\nsummary: Shadowed.\nroles:\n  x: {}\n") // shadows the built-in
	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := Load(dir, "mine")
	if err != nil || m.Roles["only"].Instructions != "Do it." || m.Taskforce == nil {
		t.Fatalf("mine: %+v %v", m, err)
	}
	if m, err := Load(dir, "slp"); err != nil || m.Summary != "Shadowed." {
		t.Fatalf("shadowed slp: %+v %v", m, err)
	}
	for _, bad := range []string{"", "../etc", "a/b", "nope"} {
		if _, err := Load(dir, bad); err == nil {
			t.Errorf("Load(%q) accepted", bad)
		}
	}
	list, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Info{}
	for _, i := range list {
		byName[i.Name] = i
	}
	if i := byName["mine"]; i.Origin != "yours" || !i.Taskforce {
		t.Errorf("mine: %+v", i)
	}
	if i := byName["broken"]; i.Origin != "yours" || !strings.HasPrefix(i.Summary, "error:") {
		t.Errorf("broken: %+v", i)
	}
	if i := byName["slp"]; i.Origin != "yours" {
		t.Errorf("slp not shadowed: %+v", i)
	}
	if i := byName["dual-lens"]; i.Origin != "built-in" {
		t.Errorf("dual-lens: %+v", i)
	}

	if _, err := CopyBuiltin(dir, "nope", "x"); err == nil {
		t.Error("copied a missing built-in")
	}
	if _, err := CopyBuiltin(dir, "slp", "mine"); err == nil {
		t.Error("copied over an existing template")
	}
}

func TestRoutingFirstMatchAllowAndCc(t *testing.T) {
	m, err := Parse([]byte(`template: r
auto_join_role: lead
roles:
  lead: {tools: [send]}
  peer: {tools: [send]}
  qa: {tools: [send]}
routing:
  - {from: peer, to: lead, cc: [qa]}
  - {from: peer, to: peer, allow: false}
  - {from: peer, to: peer}
  - {from: lead, to: peer, allow: true}
`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Allows("peer", "lead") || m.Allows("peer", "peer") || !m.Allows("lead", "peer") || m.Allows("qa", "lead") {
		t.Fatal("first match")
	}
	if cc := m.Cc("peer", "lead"); len(cc) != 1 || cc[0] != "qa" {
		t.Fatal(cc)
	}
	if m.Cc("peer", "peer") != nil || m.Cc("qa", "lead") != nil {
		t.Fatal("cc of a denied or missing rule")
	}
	if got := m.Targets("peer"); len(got) != 1 || got[0] != "lead" {
		t.Fatalf("Targets(peer) = %v", got)
	}
	if _, err := Parse([]byte("template: r\nroles:\n  a: {}\nrouting:\n  - {from: a, to: a, cc: [ghost]}\n"), nil); err == nil {
		t.Fatal("cc to a missing role accepted")
	}
}

func TestTimersAndLimits(t *testing.T) {
	m, err := Parse([]byte("template: x\nauto_join_role: a\nroles:\n  a: {can_spawn: [b]}\n  b: {}\ntimers:\n  - {on: b, silent_for: 20m, notify: reports_to}\n  - {on: b, silent_for: 1h, notify: a}\nlimits: {depth: 2, concurrency: 3, messages_per_participant_per_minute: none, max_respawn_per_hour: 4}\ntaskforce: {idle_for: 5m}\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Timers[0].Silence() != 20*time.Minute || m.Limits.MessagesPerMinute != 0 || m.Limits.MaxRespawnPerHour != 4 {
		t.Fatalf("%+v %+v", m.Timers, m.Limits)
	}
	for name, text := range map[string]string{
		"spawn without limits": "template: x\nauto_join_role: a\nroles:\n  a: {can_spawn: [b]}\n  b: {}\n",
		"timer on ghost":       "template: x\nroles:\n  a: {}\ntimers:\n  - {on: z, silent_for: 1m, notify: a}\n",
		"timer notify ghost":   "template: x\nroles:\n  a: {}\ntimers:\n  - {on: a, silent_for: 1m, notify: z}\n",
		"timer bad duration":   "template: x\nroles:\n  a: {}\ntimers:\n  - {on: a, silent_for: soon, notify: a}\n",
		"bad idle_for":         "template: x\nroles:\n  a: {}\ntaskforce: {idle_for: -1m}\n",
		"bad limit":            "template: x\nroles:\n  a: {}\nlimits: {depth: lots}\n",
	} {
		if _, err := Parse([]byte(text), nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
