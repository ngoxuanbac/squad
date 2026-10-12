package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureAndLoadConfig(t *testing.T) {
	dir := t.TempDir()
	set, err := LoadConfig(dir) // no file: the defaults
	if err != nil || set.Harness != "claude" || set.GCClosedAfter != 14*24*time.Hour {
		t.Fatalf("%+v %v", set, err)
	}
	if err := EnsureConfig(dir); err != nil {
		t.Fatal(err)
	}
	set, err = LoadConfig(dir) // the written file is the defaults too
	if err != nil {
		t.Fatal(err)
	}
	d := Defaults()
	if set.Harness != d.Harness || set.GCClosedAfter != d.GCClosedAfter || set.GCArchiveKeep != d.GCArchiveKeep ||
		len(set.Prompts) != 1 || set.Prompts[0].File != "rules/general-policy.md" || len(set.AllowedRoots) != 0 {
		t.Fatalf("written config: %+v", set)
	}
	if _, err := os.Stat(filepath.Join(dir, "rules", "general-policy.md")); err != nil {
		t.Fatal(err)
	}
	// An existing file is never changed.
	_ = os.WriteFile(ConfigPath(dir), []byte("harness: codex\n"), 0o600)
	if err := EnsureConfig(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(ConfigPath(dir)); string(b) != "harness: codex\n" {
		t.Fatalf("overwritten: %q", b)
	}
	if err := EnsureConfig(filepath.Join(ConfigPath(dir), "sub")); err == nil {
		t.Fatal("config under a file")
	}
}

func TestLoadConfigValues(t *testing.T) {
	abs := t.TempDir()
	cases := []struct {
		yaml, err string
		check     func(Settings) bool
	}{
		{"", "", func(s Settings) bool { return s.Harness == "claude" }},
		{"harness: codex\ngc:\n  closed_after: off\n  archive_keep: 36h\n", "", func(s Settings) bool {
			return s.Harness == "codex" && s.GCClosedAfter == 0 && s.GCArchiveKeep == 36*time.Hour
		}},
		{"spawn:\n  allowed_roots: [" + quote(abs) + "]\nprompts: []\n", "", func(s Settings) bool {
			return len(s.AllowedRoots) == 1 && len(s.Prompts) == 0
		}},
		{"harness: ''\n", "harness is empty", nil},
		{"colour: red\n", "colour", nil},
		{"gc:\n  closed_after: soon\n", "gc.closed_after", nil},
		{"gc:\n  archive_keep: -1d\n", "gc.archive_keep", nil},
		{"spawn:\n  allowed_roots: [rel/dir]\n", "not an absolute", nil},
		{"harness: [\n", "config.yaml", nil},
	}
	for _, c := range cases {
		dir := t.TempDir()
		_ = os.WriteFile(ConfigPath(dir), []byte(c.yaml), 0o600)
		set, err := LoadConfig(dir)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Fatalf("%q: want %q, got %v", c.yaml, c.err, err)
			}
			continue
		}
		if err != nil || !c.check(set) {
			t.Fatalf("%q: %+v %v", c.yaml, set, err)
		}
	}
	dir := t.TempDir()
	_ = os.Mkdir(ConfigPath(dir), 0o700) // a directory: unreadable as a file
	if _, err := LoadConfig(dir); err == nil {
		t.Fatal("config.yaml as a directory")
	}
}

func quote(s string) string { return "'" + s + "'" }

func TestParseRetention(t *testing.T) {
	for in, want := range map[string]time.Duration{"14d": 14 * 24 * time.Hour, "36h": 36 * time.Hour, " off ": 0, "90m": 90 * time.Minute} {
		if got, err := ParseRetention(in); err != nil || got != want {
			t.Fatalf("%q: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"", "0d", "xd", "-2h", "0s", "soon"} {
		if _, err := ParseRetention(in); err == nil {
			t.Fatalf("%q accepted", in)
		}
	}
}

func TestPromptsFor(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.md"), []byte("rule A\n"), 0o600)
	abs := filepath.Join(t.TempDir(), "b.md")
	_ = os.WriteFile(abs, []byte("rule B"), 0o600)
	var warned []string
	f := PromptsFor(dir, []PromptEntry{
		{File: "a.md", Roles: []string{"lead-peer/*"}},
		{File: abs, Roles: []string{"peer", "solo"}},
		{File: "a.md", Roles: []string{"*"}}, // a file joins once
		{File: "missing.md", Roles: []string{"slp/lead"}},
	}, func(s string) { warned = append(warned, s) })

	if got := f("lead-peer", "lead"); !strings.Contains(got, "## Shared prompt from a.md\nrule A\n") || strings.Contains(got, "rule B") {
		t.Fatalf("lead-peer/lead: %q", got)
	}
	if got := f("lead-peer", "peer"); strings.Count(got, "rule A") != 1 || !strings.Contains(got, "rule B") {
		t.Fatalf("lead-peer/peer: %q", got)
	}
	if got := f("", "solo"); !strings.Contains(got, "rule B") || !strings.Contains(got, "rule A") {
		t.Fatalf("solo: %q", got)
	}
	if got := f("slp", "lead"); strings.Contains(got, "missing") || len(warned) != 1 {
		t.Fatalf("slp/lead: %q %v", got, warned)
	}
	if got := PromptsFor(dir, nil, nil)("x", "y"); got != "" {
		t.Fatalf("none: %q", got)
	}
}
