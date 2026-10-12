package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bacnx/squad/internal/core"
	"github.com/bacnx/squad/internal/proto"
)

// `squad hook claude <HookEvent>` is the hook half of the Claude Code session adapter (the plugin
// `squad setup claude` installs): Claude runs it for each hook with the event's JSON on stdin; it
// maps the event to one standard harness event, tells the daemon, and prints what Claude should
// do with the answer. It keeps no state and never makes Claude fail: an unknown event, no daemon,
// or any error prints nothing and exits 0. In a worker (SQUAD_WORKER=1) it does nothing: a
// worker's mail goes through its stdin.

// claudeHookInput is the part of a hook's stdin squad reads.
type claudeHookInput struct {
	SessionID    string `json:"session_id"`
	Cwd          string `json:"cwd"`
	Source       string `json:"source"`            // SessionStart: startup | resume | clear | compact
	ToolName     string `json:"tool_name"`         // PreToolUse
	Notification string `json:"notification_type"` // Notification: idle_prompt, permission_prompt, …
	Reason       string `json:"reason"`            // SessionEnd: clear, prompt_input_exit, …
	PromptID     string `json:"prompt_id"`
	Model        string `json:"model"`
	// AgentID is set only when the hook fires inside a subagent: its tool calls are no boundary of
	// the session's turn (mail given there would reach the subagent, not the model that reads it).
	AgentID string `json:"agent_id"`
}

// claudeSessionHooks are the Claude hooks the plugin installs.
var claudeSessionHooks = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "StopFailure",
	"PermissionRequest", "Notification", "SessionEnd", "PreToolUse"}

// squadToolPrefix is how Claude names squad's tools (MCP server "squad").
const squadToolPrefix = "mcp__squad__"

// claudeHookEvent maps a Claude hook to the standard event (false: not one squad follows).
func claudeHookEvent(hook string, in claudeHookInput) (core.HarnessEventArgs, bool) {
	a := core.HarnessEventArgs{Key: in.SessionID, Harness: "claude", PromptID: in.PromptID}
	if in.AgentID != "" && hook != "PermissionRequest" {
		return a, false
	}
	switch hook {
	case "SessionStart":
		a.Event, a.Source, a.Cwd, a.Model = core.HarnessSessionStart, in.Source, in.Cwd, in.Model
	case "UserPromptSubmit":
		a.Event = core.HarnessTurnStart
	case "PostToolUse":
		a.Event = core.HarnessToolBoundary
	case "Stop":
		a.Event, a.OK = core.HarnessTurnEnd, true
	case "StopFailure":
		a.Event = core.HarnessTurnEnd
	case "PermissionRequest":
		a.Event = core.HarnessPermission
	case "Notification":
		if in.Notification != "idle_prompt" {
			return a, false
		}
		a.Event = core.HarnessIdle // Claude waits on its person: a turn left open by Esc is over
	case "SessionEnd":
		if in.Reason == "clear" {
			return a, false // /clear goes on in the same process: the participant lives on
		}
		a.Event = core.HarnessSessionEnd
	default:
		return a, false
	}
	return a, a.Key != ""
}

// claudeHookOutput is the JSON Claude reads from the hook's stdout (nil: print nothing). Mail goes
// in as additionalContext where the event takes it; at a turn's end, block keeps the turn going
// with the mail as the reason.
func claudeHookOutput(hook string, r core.HarnessEventResult) any {
	if r.Text == "" {
		return nil
	}
	switch hook {
	case "Stop":
		if r.Block {
			return map[string]any{"decision": "block", "reason": r.Text}
		}
	case "SessionStart", "UserPromptSubmit", "PostToolUse":
		return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": hook, "additionalContext": r.Text}}
	}
	return nil
}

// runHook handles one hook; harness is "claude".
func runHook(harness, hook string, in io.Reader, out io.Writer, getenv func(string) string) error {
	if harness != "claude" {
		return errors.New("usage: squad hook claude <HookEvent>")
	}
	if getenv("SQUAD_WORKER") == "1" {
		return nil
	}
	var input claudeHookInput
	raw, _ := io.ReadAll(in)
	_ = json.Unmarshal(raw, &input)
	if path := getenv("SQUAD_HOOK_LOG"); path != "" { // debugging: what Claude passed, one line per hook
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(f, "%s %s\n", hook, bytes.TrimSpace(raw))
			f.Close()
		}
	}
	if hook == "PreToolUse" {
		// squad's own tools need no permission prompt (the plugin's matcher is mcp__squad__.*).
		if strings.HasPrefix(input.ToolName, squadToolPrefix) {
			_, _ = io.WriteString(out, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}`+"\n")
		}
		return nil
	}
	a, ok := claudeHookEvent(hook, input)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var res core.HarnessEventResult
	if err := call(ctx, proto.VerbHarnessEvent, "", a, &res); err != nil {
		return nil // no daemon, or not known to squad: Claude goes on as if squad were not there
	}
	if v := claudeHookOutput(hook, res); v != nil {
		b, _ := json.Marshal(v)
		_, _ = out.Write(append(b, '\n'))
	}
	return nil
}
