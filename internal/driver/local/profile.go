package local

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Inherit, as a profile's model or thinking, leaves the choice to the next link of the chain.
const Inherit = "inherit"

// ClaudeProfile is ~/.squad/harness/claude.json: how Claude workers start. It is read at each
// spawn and resume, so a change applies to the next worker.
type ClaudeProfile struct {
	Cmd  string   `json:"cmd"`  // the claude command
	Args []string `json:"args"` // extra flags
	Env  []string `json:"env"`  // extra KEY=VALUE
	// Model and Thinking (Claude's effort level) for workers whose role and spawn name none.
	Model    string `json:"model"`
	Thinking string `json:"thinking"`
	// DisallowedTools are Claude's own tools a worker must not have (--disallowedTools): those
	// that wait on a person, start agents or schedule turns outside squad. A role keeps one with
	// spawn.allow_tools.
	DisallowedTools []string `json:"disallowed_tools"`
	// Blacklist names parts of your Claude setup a worker must not load: MCP servers of
	// ~/.claude.json by name, and plugins as name@marketplace.
	Blacklist []string `json:"blacklist"`
}

// DefaultClaudeProfile is the profile written when there is none, and the value of every key a
// profile leaves out.
var DefaultClaudeProfile = ClaudeProfile{
	Cmd: "claude", Args: []string{}, Env: []string{}, Model: Inherit, Thinking: Inherit,
	DisallowedTools: []string{"Agent", "Task", "SendMessage", "ListAgents", "Workflow", "CronCreate", "ScheduleWakeup",
		"Monitor", "RemoteTrigger", "AskUserQuestion", "EnterPlanMode", "ExitPlanMode", "EnterWorktree", "ExitWorktree",
		"PushNotification", "SendUserFile", "Artifact", "ShareOnboardingGuide", "SendFeedback"},
	Blacklist: []string{},
}

// ClaudeProfilePath is where the Claude profile lives under squad's home.
func ClaudeProfilePath(dir string) string { return filepath.Join(dir, "harness", "claude.json") }

// EnsureProfiles writes the default Claude profile when there is none. An existing profile is
// never changed.
func EnsureProfiles(dir string) error {
	path := ClaudeProfilePath(dir)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(DefaultClaudeProfile, "", "  ")
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// ReadClaudeProfile reads the profile over the defaults: a key it leaves out keeps its default.
// A missing file is the defaults.
func ReadClaudeProfile(dir string) (ClaudeProfile, error) {
	p := DefaultClaudeProfile
	p.Args, p.Env = []string{}, []string{}
	p.DisallowedTools = append([]string{}, DefaultClaudeProfile.DisallowedTools...)
	p.Blacklist = []string{}
	b, err := os.ReadFile(ClaudeProfilePath(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("Claude profile %s: %w", ClaudeProfilePath(dir), err)
	}
	if p.Cmd == "" {
		p.Cmd = DefaultClaudeProfile.Cmd
	}
	return p, nil
}

// inherited is v, or "" when it leaves the choice to the next link.
func inherited(v string) string {
	if v == Inherit {
		return ""
	}
	return v
}
