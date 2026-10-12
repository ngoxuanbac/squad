// Package proto is the wire format shared by the daemon and its clients (the CLI, `squad mcp`):
// one JSON Request per HTTP POST to /rpc on 127.0.0.1, one Response back. Args and Result are the
// core argument and result types marshaled as-is.
package proto

import "encoding/json"

// Verbs. Admin verbs require the admin token from daemon.json; the rest a participant token.
const (
	// Admin.
	VerbPing      = "ping"
	VerbRegister  = "register" // a session's `squad mcp` joins squad
	VerbView      = "view"     // ps --view
	VerbResolve   = "resolve"  // a CLI argument to a participant
	VerbKill      = "kill"
	VerbInterrupt = "interrupt"
	VerbResume    = "resume"
	VerbTeamDown  = "team_down"
	VerbShutdown  = "shutdown"
	// VerbHarnessEvent is a session's hook reporting its turn (core.HarnessEventArgs); the hook has
	// no token of its own, the session's harness id names the session.
	VerbHarnessEvent = "harness_event"
	// VerbRelease delivers a message held by a limit (name: its #N).
	VerbRelease = "release"
	// Diagnostics: every check of a send (name: from, to), the event log (after, team, limit), and
	// what looks stuck.
	VerbWhy    = "why"
	VerbLog    = "log"
	VerbDoctor = "doctor"
	// VerbModel sets a worker's model or thinking level (name, model, thinking).
	VerbModel = "model"
	// VerbGC archives and deletes closed teams and gone solos (before, dry_run).
	VerbGC = "gc"

	// Participant.
	VerbWhoami     = "whoami"
	VerbHeartbeat  = "heartbeat"
	VerbUnregister = "unregister"
	VerbSend       = "send"
	VerbInbox      = "inbox"
	VerbWho        = "who"
	VerbAgent      = "agent"
	// VerbWakeWait is a hooked session's squad mcp waiting to be told to wake it (wait_seconds).
	VerbWakeWait = "wake_wait"
)

// ParticipantVerbs take a participant's token. Every other verb needs the admin token, one added
// later too.
var ParticipantVerbs = map[string]bool{VerbWhoami: true, VerbHeartbeat: true, VerbUnregister: true, VerbSend: true,
	VerbInbox: true, VerbWho: true, VerbAgent: true, VerbWakeWait: true}

// Request is one call.
type Request struct {
	Op    string          `json:"op"`
	Token string          `json:"token"`
	Args  json.RawMessage `json:"args"`
}

// Response is its answer: Result when OK, else a Code the tools pass on to the agent and an Error.
type Response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Code   string          `json:"code,omitempty"`
	Rule   string          `json:"rule,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Info is daemon.json: where the running daemon listens, and its admin token.
type Info struct {
	Addr    string `json:"addr"`
	Pid     int    `json:"pid"`
	Token   string `json:"token"`
	Version string `json:"version"`
}

// Args is the union of the simple verbs' arguments.
type Args struct {
	Cwd      string `json:"cwd,omitempty"`     // register
	Key      string `json:"key,omitempty"`     // register: the harness's session id
	Harness  string `json:"harness,omitempty"` // register: the session's harness
	Model    string `json:"model,omitempty"`   // register, model
	To       string `json:"to,omitempty"`      // why: the recipient
	Wait     int    `json:"wait_seconds,omitempty"`
	View     string `json:"view,omitempty"`     // inbox: board
	After    int64  `json:"after,omitempty"`    // log
	Team     string `json:"team,omitempty"`     // log
	Limit    int    `json:"limit,omitempty"`    // log
	Name     string `json:"name,omitempty"`     // resolve, kill, interrupt, resume, team_down, model, release (#N), why (the sender)
	Thinking string `json:"thinking,omitempty"` // model
	Before   string `json:"before,omitempty"`   // gc: closed or gone longer than this (14d, 36h)
	DryRun   bool   `json:"dry_run,omitempty"`  // gc
}

// DownMessage is what a client says when no daemon answers. The Paseo plugin looks for it.
const DownMessage = "the squad daemon is not running"
