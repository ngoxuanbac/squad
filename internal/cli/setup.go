package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/bacnx/squad/internal/driver/local"
	"github.com/bacnx/squad/internal/server"
)

func setup(args []string) error {
	switch {
	case len(args) == 0:
		return setupStatus()
	case len(args) == 1 && args[0] == "claude":
		return setupClaude()
	case len(args) == 1 && args[0] == "paseo":
		return setupPaseo()
	case len(args) == 2 && args[0] == "remove" && args[1] == "claude":
		return removeClaude()
	case len(args) == 2 && args[0] == "remove" && args[1] == "paseo":
		return removePaseo()
	default:
		return errors.New("usage: squad setup [claude|paseo] | squad setup remove claude|paseo")
	}
}

func setupStatus() error {
	if out, err := exec.Command("claude", "--version").Output(); err == nil {
		state := "not added (squad setup claude)"
		if exec.Command("claude", "mcp", "get", "squad").Run() == nil {
			state = "MCP server squad added"
			switch _, installed, enabled := claudePluginState(); {
			case installed && enabled:
				state += ", plugin " + local.ClaudeSquadPlugin + " enabled"
			case installed:
				state += ", plugin " + local.ClaudeSquadPlugin + " disabled (claude plugin enable " + local.ClaudeSquadPlugin + ")"
			default:
				state += ", no hooks plugin: sessions are not woken (squad setup claude)"
			}
		}
		fmt.Printf("claude  %s: %s\n", strings.TrimSpace(string(out)), state)
	} else {
		fmt.Println("claude  not found on PATH: workers need Claude Code")
	}
	if bin := paseoBin(); bin != "" {
		state := "plugin not installed (squad setup paseo)"
		if paseoHasSquad(bin) {
			state = "plugin squad installed"
		}
		fmt.Printf("paseo   %s: %s\n", bin, state)
	} else {
		fmt.Println("paseo   not found")
	}
	fmt.Println("home   ", server.Dir())
	return nil
}

// removeClaude takes the MCP server and the hooks plugin out of Claude Code, and deletes the
// plugin's files. Workers are unaffected: the daemon gives each its own MCP config.
func removeClaude() error {
	if _, err := exec.LookPath("claude"); err != nil {
		return errors.New("claude not found on PATH")
	}
	done := unsetClaude()
	if err := os.RemoveAll(claudePluginRoot(server.Dir())); err != nil {
		return err
	}
	if len(done) == 0 {
		fmt.Println("squad was not in Claude Code")
		return nil
	}
	fmt.Println("removed from Claude Code:", strings.Join(done, ", ")+". Restart open Claude Code sessions.")
	return nil
}

// removePaseo takes the squad plugin out of Paseo and deletes its files.
func removePaseo() error {
	bin := paseoBin()
	if bin == "" {
		return errors.New("paseo not found")
	}
	removed := false
	if paseoHasSquad(bin) {
		if err := exec.Command(bin, "plugin", "remove", "squad").Run(); err != nil {
			return fmt.Errorf("paseo plugin remove squad: %w", err)
		}
		removed = true
	}
	if err := os.RemoveAll(paseoDir(server.Dir())); err != nil {
		return err
	}
	if !removed {
		fmt.Println("the squad plugin was not in Paseo")
		return nil
	}
	fmt.Println("squad plugin removed from Paseo; reload the Paseo app.")
	return nil
}

// unsetClaude takes squad's MCP server, plugin and marketplace out of Claude Code, and says which
// it took.
func unsetClaude() (done []string) {
	if exec.Command("claude", "mcp", "remove", "--scope", "user", "squad").Run() == nil {
		done = append(done, "MCP server squad")
	}
	market, installed, _ := claudePluginState()
	if installed && exec.Command("claude", "plugin", "uninstall", local.ClaudeSquadPlugin).Run() == nil {
		done = append(done, "plugin "+local.ClaudeSquadPlugin)
	}
	if market && exec.Command("claude", "plugin", "marketplace", "remove", claudeMarketplace).Run() == nil {
		done = append(done, "marketplace "+claudeMarketplace)
	}
	return done
}

// paseoHasSquad says whether Paseo has a plugin named squad.
func paseoHasSquad(bin string) bool {
	out, err := exec.Command(bin, "plugin", "ls", "--json").Output()
	return err == nil && strings.Contains(string(out), `"squad"`)
}
