package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bacnx/squad/internal/driver/local"
	"github.com/bacnx/squad/internal/server"
)

// setup claude: squad mcp as a user-scope MCP server of Claude Code, and a hooks-only plugin
// (`squad@squad`, from a local marketplace in ~/.squad/claude) that gives a session its mail with
// its turns. Claude's own commands do the installing; squad writes nothing in ~/.claude.

const claudeMarketplace = "squad"

// claudePluginRoot is the local marketplace setup claude writes.
func claudePluginRoot(dir string) string { return filepath.Join(dir, "claude") }

// hookCommand is how the plugin runs `squad hook claude <event>`: the binary's path with forward
// slashes in double quotes, which the shells Claude runs hooks in on Windows and Unix all take.
func hookCommand(self, event string) string {
	return `"` + filepath.ToSlash(self) + `" hook claude ` + event
}

// writeClaudePlugin writes the marketplace and its hooks-only plugin under root.
func writeClaudePlugin(root, self string) error {
	hooks := map[string]any{}
	for _, ev := range claudeSessionHooks {
		m := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": hookCommand(self, ev), "timeout": 10}}}
		switch ev {
		case "PreToolUse":
			m["matcher"] = squadToolPrefix + ".*"
		case "Notification":
			m["matcher"] = "idle_prompt"
		}
		hooks[ev] = []any{m}
	}
	files := []struct {
		path string
		v    any
	}{
		{filepath.Join(root, ".claude-plugin", "marketplace.json"), map[string]any{"name": claudeMarketplace,
			"owner":   map[string]any{"name": "squad"},
			"plugins": []any{map[string]any{"name": "squad", "source": "./squad", "description": "squad hooks for Claude Code sessions"}}}},
		{filepath.Join(root, "squad", ".claude-plugin", "plugin.json"), map[string]any{"name": "squad", "version": Version,
			"description": "Joins this Claude Code session to squad: mail with its turns, tool calls and the end of a turn"}},
		{filepath.Join(root, "squad", "hooks", "hooks.json"), map[string]any{"hooks": hooks}},
	}
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
			return err
		}
		b, _ := json.MarshalIndent(f.v, "", "  ")
		if err := os.WriteFile(f.path, append(b, '\n'), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// claudeJSON runs a claude command that prints JSON and decodes it into out.
func claudeJSON(out any, args ...string) error {
	b, err := exec.Command("claude", args...).Output()
	if err != nil {
		return fmt.Errorf("claude %s: %w", strings.Join(args, " "), err)
	}
	return json.Unmarshal(b, out)
}

// claudePluginState is what Claude has of squad's marketplace and plugin.
func claudePluginState() (market, installed, enabled bool) {
	var ms []struct{ Name string }
	if claudeJSON(&ms, "plugin", "marketplace", "list", "--json") == nil {
		for _, m := range ms {
			market = market || m.Name == claudeMarketplace
		}
	}
	var ps []struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if claudeJSON(&ps, "plugin", "list", "--json") == nil {
		for _, p := range ps {
			if p.ID == local.ClaudeSquadPlugin {
				installed, enabled = true, p.Enabled
			}
		}
	}
	return
}

// setupClaude adds squad mcp and the hooks plugin; run again it installs them afresh (after the
// binary moved, or a new version).
func setupClaude() error {
	if _, err := exec.LookPath("claude"); err != nil {
		return errors.New("claude not found on PATH")
	}
	dir := server.Dir()
	root := claudePluginRoot(dir)
	exe := self()
	if err := writeClaudePlugin(root, exe); err != nil {
		return err
	}
	run := func(args ...string) error {
		cmd := exec.Command("claude", args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("claude %s: %w", strings.Join(args, " "), err)
		}
		return nil
	}
	unsetClaude()
	if err := run("mcp", "add", "--scope", "user", "squad", "--", exe, "mcp"); err != nil {
		return err
	}
	if err := run("plugin", "marketplace", "add", root); err != nil {
		return err
	}
	if err := run("plugin", "install", local.ClaudeSquadPlugin); err != nil {
		return err
	}
	fmt.Println("squad added to Claude Code (MCP server squad, plugin " + local.ClaudeSquadPlugin + "). " +
		"Restart open Claude Code sessions; then ask one: \"found a lead-peer team to ...\"")
	return nil
}
