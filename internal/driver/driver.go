// Package driver is what the engine knows of a harness: an interface. Core decides (gate, rows,
// mail); a Runtime owns the worker processes of one harness (internal/driver/local has Claude's).
// No harness name is branched on in core: a role or a session names a harness, and the engine
// looks its Runtime up by that name.
package driver

import (
	"crypto/rand"
	"fmt"
	"time"
)

// Spec is what a Runtime needs to start one run of a worker.
type Spec struct {
	ParticipantID string
	Cwd           string
	SessionRef    string // the harness's session id: created, or continued when Resume
	Resume        bool
	Model         string // "" = the harness default
	Thinking      string // "" = the harness default; in the harness's own levels
	RoleCard      string // what the worker is told about its place in the team
	Token         string // the participant's token, for its squad MCP server
	AllowTools    []string
	LogPath       string // the run's output is appended here (squad tail reads it)
	RunDir        string // scratch for this participant's runs
}

// Events are what a Run tells the engine, from the run's own goroutines.
type Events interface {
	// Working reports a turn starting (true) or ending (false).
	Working(participantID string, working bool)
	// Completed reports the end of the delivered batch: ok, its mail may be acked; not ok
	// (cancelled), it is given again.
	Completed(participantID string, batch int64, ok bool)
	// Exited reports the process gone; err is how it ended.
	Exited(participantID string, err error)
}

// Run is one live worker process.
type Run interface {
	Pid() int
	// Deliver hands the worker one batch of mail; idle, it starts a turn. The end of the batch
	// comes back as Events.Completed.
	Deliver(batch int64, text string) error
	// Interrupt cancels the current turn; the worker stays alive.
	Interrupt() error
	// Configure switches the running worker's model, thinking level or both ("" keeps it) from
	// its next turn, its context kept; it returns once the harness took them, or with its refusal.
	Configure(model, thinking string) error
	// Stop ends the process tree, politely first, for good within grace.
	Stop(grace time.Duration)
}

// Runtime starts workers of one harness.
type Runtime interface {
	// Harness is the name roles and sessions use for it ("claude").
	Harness() string
	// Defaults are the model and thinking level its profile sets ("" none): the last links of a
	// worker's chain before the harness's own default.
	Defaults() (model, thinking string)
	// Levels are the thinking levels it takes (nil: any); a level it would not run is refused
	// before it is stored.
	Levels() []string
	// NewSessionRef is a fresh session id in the harness's format.
	NewSessionRef() string
	// Start launches a run.
	Start(s Spec, ev Events) (Run, error)
}

// NewUUID is a random version 4 UUID.
func NewUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
