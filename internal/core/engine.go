// Package core is the squad engine: registry, mailbox and the gate that checks every
// mail and every spawn against the team's template. Every change runs under one lock.
package core

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bacnx/squad/internal/driver"
	"github.com/bacnx/squad/internal/store"
	"github.com/bacnx/squad/manifests"
)

// Participant kinds and statuses.
const (
	KindSession = "session" // an agent the human opened (its `squad mcp` registered it)
	KindWorker  = "worker"  // a headless Claude squad started

	StatusLive    = "idle"    // a live session or an idle worker
	StatusWorking = "working" // a worker in a turn
	StatusStopped = "stopped" // a worker whose process is not running
	StatusGone    = "gone"    // a session that went away
	// StatusPermission: a hooked session waiting on its person's answer to a permission prompt;
	// its mail waits, and it is not woken.
	StatusPermission = "permission"
	// StatusParked: a worker resumed more often than its team allows in an hour; mail does not
	// resume it.
	StatusParked = "parked"

	TeamOpen   = "open"
	TeamClosed = "closed"
)

// Timing.
const (
	SessionTimeout = 45 * time.Second // a session without a heartbeat for this long is gone
	StopGrace      = 4 * time.Second
)

// Error is a refusal with a code the tools pass on to the agent.
type Error struct {
	Code string `json:"code"`
	Msg  string `json:"error"`
	// Rule names the rule that refused (routing[2], assign.not_reports_to, …), when one did.
	Rule string `json:"rule,omitempty"`
}

func (e *Error) Error() string { return e.Msg }

func errf(code, format string, a ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// denyf is a refusal by a named rule.
func denyf(code, rule, format string, a ...any) error {
	return &Error{Code: code, Rule: rule, Msg: fmt.Sprintf(format, a...)}
}

// Participant is one row of participants.
type Participant struct {
	ID, Name, TeamID, Role, Kind, Status, Cwd, SessionRef, SpawnedBy, Model, Thinking, Token, Task string
	Depth                                                                                          int
	Respawns, CreatedAt, LastActive, StateSince, LastTurnEnd                                       string
	Harness                                                                                        string // the Runtime that runs it (workers), or the harness of a session
	// A session with its harness's hooks (Hooked) has turns: Turn is the open one's key ('' none),
	// TurnSeq numbers them, TurnBlocks counts the open turn's held ends.
	Hooked bool
	// WasSession: a session you opened, run as a headless worker since its mail woke it while it
	// was gone (WokeMail: the newest mail that did); opening it again takes it back.
	WasSession          bool
	WokeMail            int64
	Turn                string
	TurnSeq, TurnBlocks int
}

// Team is one row of teams with its frozen manifest.
type Team struct {
	ID, Name, Template, Root, GateID, ParentID, Status, CreatedAt string
	M                                                             *manifests.Manifest
}

// Engine owns the database and the live worker runs.
type Engine struct {
	mu      sync.Mutex
	db      *sql.DB
	dir     string // squad's home: database, logs, templates
	version string
	started time.Time

	runtimes       map[string]driver.Runtime // by harness name
	defaultHarness string
	runs           map[string]driver.Run // participant id -> live run
	waiters        map[string]*waiter
	allowedRoots   []string
	prompts        func(template, role string) string
	wakers         map[string]chan struct{} // a hooked session's squad mcp, waiting to wake it
	lastWake       map[string]time.Time
	closing        bool
}

// Options configure an engine.
type Options struct {
	Dir     string // squad's home (server.Dir)
	Version string
	// Runtimes run the workers, one per harness. DefaultHarness names the one for a worker whose
	// role and founding session name none ("": the first).
	Runtimes       []driver.Runtime
	DefaultHarness string
	// AllowedRoots are directories outside a team's root where a worker may work.
	AllowedRoots []string
	// Prompts is the text of your shared prompts for a role of a template (template "", role
	// "solo": a solo session); nil: none.
	Prompts func(template, role string) string
}

// Open opens the engine on the database in o.Dir. Workers of a previous daemon are marked stopped (their
// processes died with it) and their delivered but unacked mail is given again when they resume.
func Open(o Options) (*Engine, error) {
	for _, d := range []string{o.Dir, LogsDir(o.Dir), TemplatesDir(o.Dir)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	db, err := store.Open(DBPath(o.Dir))
	if err != nil {
		return nil, err
	}
	e := &Engine{db: db, dir: o.Dir, version: o.Version, started: time.Now().UTC(),
		runtimes: map[string]driver.Runtime{}, defaultHarness: o.DefaultHarness, allowedRoots: o.AllowedRoots, prompts: o.Prompts,
		runs: map[string]driver.Run{}, waiters: map[string]*waiter{}, wakers: map[string]chan struct{}{}, lastWake: map[string]time.Time{}}
	for _, r := range o.Runtimes {
		e.runtimes[r.Harness()] = r
		if e.defaultHarness == "" {
			e.defaultHarness = r.Harness()
		}
	}
	if _, err := db.Exec(`UPDATE participants SET status=? WHERE kind=? AND status IN (?,?)`,
		StatusStopped, KindWorker, StatusLive, StatusWorking); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`UPDATE mail SET delivered_at='', batch=0 WHERE acked_at='' AND delivered_at!=''`); err != nil {
		return nil, err
	}
	return e, nil
}

// Close stops every worker and closes the database.
func (e *Engine) Close() {
	e.mu.Lock()
	e.closing = true
	runs := make([]driver.Run, 0, len(e.runs))
	for _, r := range e.runs {
		runs = append(runs, r)
	}
	e.mu.Unlock()
	var wg sync.WaitGroup
	for _, r := range runs {
		wg.Add(1)
		go func() { defer wg.Done(); r.Stop(StopGrace) }()
	}
	wg.Wait()
	e.db.Close()
}
func now() string { return time.Now().UTC().Format(time.RFC3339) }

func newID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

func newToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

var nameClean = regexp.MustCompile(`[^a-z0-9-]+`)

// cleanName makes a name squad can show and agents can type.
func cleanName(s string) string {
	s = nameClean.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	s = strings.Trim(s, "-")
	if len(s) > 32 {
		s = strings.Trim(s[:32], "-")
	}
	return s
}

// reserved names cannot be used for participants or teams.
var reserved = map[string]bool{"all": true, "human": true, "squad": true, "caller": true, EngineID: true, AddrBoard: true, AddrNotify: true}

// --- reads (callers hold e.mu) ---

const pCols = `id,name,team_id,role,kind,status,cwd,session_ref,spawned_by,model,thinking,token,task,depth,respawns,created_at,last_active,state_since,last_turn_end,harness,hooked,turn,turn_seq,turn_blocks,was_session,woke_mail`

func scanP(row interface{ Scan(...any) error }) (*Participant, error) {
	var p Participant
	err := row.Scan(&p.ID, &p.Name, &p.TeamID, &p.Role, &p.Kind, &p.Status, &p.Cwd, &p.SessionRef, &p.SpawnedBy,
		&p.Model, &p.Thinking, &p.Token, &p.Task, &p.Depth, &p.Respawns, &p.CreatedAt, &p.LastActive, &p.StateSince, &p.LastTurnEnd, &p.Harness, &p.Hooked, &p.Turn, &p.TurnSeq, &p.TurnBlocks, &p.WasSession, &p.WokeMail)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (e *Engine) pWhere(where string, args ...any) (*Participant, error) {
	p, err := scanP(e.db.QueryRow(`SELECT `+pCols+` FROM participants WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func (e *Engine) pList(where string, args ...any) ([]*Participant, error) {
	rows, err := e.db.Query(`SELECT `+pCols+` FROM participants WHERE `+where+` ORDER BY created_at, name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Participant
	for rows.Next() {
		p, err := scanP(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (e *Engine) byID(id string) (*Participant, error) {
	if id == "" {
		return nil, nil
	}
	return e.pWhere(`id=?`, id)
}

// byToken is the caller of a tool call.
func (e *Engine) byToken(token string) (*Participant, error) {
	if token == "" {
		return nil, errf("auth", "no token")
	}
	p, err := e.pWhere(`token=?`, token)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, errf("auth", "unknown token: this squad session is not registered (restart the agent)")
	}
	return p, nil
}

const tCols = `id,name,template,root,manifest,gate_id,parent_id,status,created_at`

func scanT(row interface{ Scan(...any) error }) (*Team, error) {
	var t Team
	var m string
	if err := row.Scan(&t.ID, &t.Name, &t.Template, &t.Root, &m, &t.GateID, &t.ParentID, &t.Status, &t.CreatedAt); err != nil {
		return nil, err
	}
	t.M = &manifests.Manifest{}
	if err := json.Unmarshal([]byte(m), t.M); err != nil {
		return nil, fmt.Errorf("team %s: manifest: %w", t.Name, err)
	}
	return &t, nil
}

func (e *Engine) tWhere(where string, args ...any) (*Team, error) {
	t, err := scanT(e.db.QueryRow(`SELECT `+tCols+` FROM teams WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

func (e *Engine) tList(where string, args ...any) ([]*Team, error) {
	rows, err := e.db.Query(`SELECT `+tCols+` FROM teams WHERE `+where+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Team
	for rows.Next() {
		t, err := scanT(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (e *Engine) team(id string) (*Team, error) {
	if id == "" {
		return nil, nil
	}
	return e.tWhere(`id=?`, id)
}

// uniqueName is base, or base-2, base-3… when a participant (other than except) or an open team
// has it.
func (e *Engine) uniqueName(base, except string) (string, error) {
	base = cleanName(base)
	if base == "" {
		base = "agent"
	}
	for i := 1; ; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		if reserved[name] {
			continue
		}
		var n int
		if err := e.db.QueryRow(`SELECT (SELECT COUNT(*) FROM participants WHERE name=? AND id!=?) + (SELECT COUNT(*) FROM teams WHERE name=? AND status=?)`,
			name, except, name, TeamOpen).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return name, nil
		}
	}
}

func (e *Engine) event(kind, team, format string, a ...any) {
	_, _ = e.db.Exec(`INSERT INTO events(at,kind,team,text) VALUES(?,?,?,?)`, now(), kind, team, fmt.Sprintf(format, a...))
}

func (e *Engine) teamName(id string) string {
	if t, _ := e.team(id); t != nil {
		return t.Name
	}
	return ""
}

func (e *Engine) nameOf(id string) string {
	if p, _ := e.byID(id); p != nil {
		return p.Name
	}
	return ""
}

func (e *Engine) touch(id string) {
	_, _ = e.db.Exec(`UPDATE participants SET last_active=? WHERE id=?`, now(), id)
}

// wake tells inbox waiters of id that mail came.
func (e *Engine) wake(id string) {
	if w, ok := e.waiters[id]; ok {
		close(w.ch)
		delete(e.waiters, id)
	}
}

func shortDir(p string) string { return filepath.Base(filepath.Clean(p)) }
