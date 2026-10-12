package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bacnx/squad/manifests"
)

// config.yaml: the daemon's settings, read at its start (a change needs `squad restart`). A key it
// does not know, or a bad value, stops the daemon; the reason is in serve.log.

// Settings are config.yaml as the daemon uses it.
type Settings struct {
	// Harness runs a worker whose role and founding session name none.
	Harness string
	// GCClosedAfter: gc archives and deletes a team closed (or a solo session gone) longer than
	// this; 0 is off.
	GCClosedAfter time.Duration
	// GCArchiveKeep: gc archives older than this are deleted; 0 is off.
	GCArchiveKeep time.Duration
	// AllowedRoots are absolute directories outside a team's root where a worker may work (cwd).
	AllowedRoots []string
	// Prompts are your own files added to the role cards of the roles they name.
	Prompts []PromptEntry
}

// PromptEntry is one shared prompt: a file of yours (relative to squad's home, or absolute) and the
// roles whose card it joins: <role> (in every template), <template>/<role>, <template>/* (every
// role of it), solo (a solo session), or * (every role, and solo sessions).
type PromptEntry struct {
	File  string   `yaml:"file"`
	Roles []string `yaml:"roles"`
}

// Defaults are the settings of a config.yaml that sets nothing.
func Defaults() Settings {
	return Settings{Harness: "claude", GCClosedAfter: 14 * 24 * time.Hour, GCArchiveKeep: 30 * 24 * time.Hour,
		AllowedRoots: []string{}, Prompts: []PromptEntry{{File: "rules/general-policy.md", Roles: []string{"lead-peer/*", "slp/*"}}}}
}

// ConfigPath is config.yaml under dir.
func ConfigPath(dir string) string { return filepath.Join(dir, "config.yaml") }

const defaultConfig = `# squad daemon settings. A change needs ` + "`squad restart`" + `; ` + "`squad check`" + ` reads this file as
# the daemon will. An unknown key or a bad value stops the daemon (the reason is in serve.log).

# The harness of a worker whose role and founding session name none.
harness: claude

gc:
  # Archive and delete a team closed (or a solo session gone) longer than this: 14d, 36h, or off.
  closed_after: 14d
  # Delete gc archives older than this, or off.
  archive_keep: 30d

spawn:
  # Absolute directories outside a team's root where a worker may work (cwd). The root and the git
  # worktrees of its repository always may.
  allowed_roots: []

# Your own files added to role cards: file is relative to squad's home or absolute; roles are
# <role>, <template>/<role>, <template>/*, solo, or *. A file is read at each start of a worker or
# session, so an edit shows in the next one.
prompts:
  - file: rules/general-policy.md
    roles: ["lead-peer/*", "slp/*"]
`

// EnsureConfig writes config.yaml and the rules file it names when they are missing; an existing
// file is never changed.
func EnsureConfig(dir string) error {
	files := map[string][]byte{ConfigPath(dir): []byte(defaultConfig)}
	rules, err := fs.ReadFile(manifests.FS, "rules/general-policy.md")
	if err != nil {
		return err
	}
	files[filepath.Join(dir, "rules", "general-policy.md")] = rules
	for path, b := range files {
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// LoadConfig reads config.yaml over the defaults (a missing file is the defaults).
func LoadConfig(dir string) (Settings, error) {
	set := Defaults()
	b, err := os.ReadFile(ConfigPath(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return set, nil
	}
	if err != nil {
		return set, err
	}
	var raw struct {
		Harness *string `yaml:"harness"`
		GC      struct {
			ClosedAfter *string `yaml:"closed_after"`
			ArchiveKeep *string `yaml:"archive_keep"`
		} `yaml:"gc"`
		Spawn struct {
			AllowedRoots []string `yaml:"allowed_roots"`
		} `yaml:"spawn"`
		Prompts *[]PromptEntry `yaml:"prompts"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		return set, fmt.Errorf("%s: %w", ConfigPath(dir), err)
	}
	if raw.Harness != nil {
		if *raw.Harness == "" {
			return set, fmt.Errorf("%s: harness is empty", ConfigPath(dir))
		}
		set.Harness = *raw.Harness
	}
	for _, d := range []struct {
		key string
		v   *string
		to  *time.Duration
	}{{"gc.closed_after", raw.GC.ClosedAfter, &set.GCClosedAfter}, {"gc.archive_keep", raw.GC.ArchiveKeep, &set.GCArchiveKeep}} {
		if d.v == nil {
			continue
		}
		v, err := ParseRetention(*d.v)
		if err != nil {
			return set, fmt.Errorf("%s: %s: %w", ConfigPath(dir), d.key, err)
		}
		*d.to = v
	}
	if raw.Spawn.AllowedRoots != nil {
		for _, r := range raw.Spawn.AllowedRoots {
			if !filepath.IsAbs(r) {
				return set, fmt.Errorf("%s: spawn.allowed_roots: %q is not an absolute directory", ConfigPath(dir), r)
			}
		}
		set.AllowedRoots = raw.Spawn.AllowedRoots
	}
	if raw.Prompts != nil {
		set.Prompts = *raw.Prompts
	}
	return set, nil
}

// ParseRetention reads 14d, 36h, 90m or off (0).
func ParseRetention(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "off" {
		return 0, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%q: want 14d, 36h or off", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q: want 14d, 36h or off", s)
	}
	return d, nil
}

// PromptsFor is the shared prompts of a role of a template (template "" and role "solo": a solo
// session), each under a heading with its file. Files are read on each call; one that cannot be
// read is left out and warned about.
func PromptsFor(dir string, entries []PromptEntry, warn func(string)) func(template, role string) string {
	return func(template, role string) string {
		var b strings.Builder
		seen := map[string]bool{}
		for _, e := range entries {
			path := e.File
			if !filepath.IsAbs(path) {
				path = filepath.Join(dir, path)
			}
			if seen[path] || !promptMatches(e.Roles, template, role) {
				continue
			}
			seen[path] = true
			text, err := os.ReadFile(path)
			if err != nil {
				warn(fmt.Sprintf("shared prompt %s left out of the card of %s/%s: %v", e.File, template, role, err))
				continue
			}
			fmt.Fprintf(&b, "\n## Shared prompt from %s\n%s\n", e.File, strings.TrimSpace(string(text)))
		}
		return b.String()
	}
}

func promptMatches(roles []string, template, role string) bool {
	for _, r := range roles {
		if r == "*" || r == role || (template != "" && (r == template+"/"+role || r == template+"/*")) {
			return true
		}
	}
	return false
}
