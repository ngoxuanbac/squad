// Package server is `squad serve`: the single daemon that owns the database and serves the core
// Engine over HTTP on 127.0.0.1 (internal/proto). The address and the admin token are in
// daemon.json under Dir(), readable only by the user. Agents call with their own participant
// token; the CLI and `squad mcp` registering a session use the admin token.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bacnx/squad/internal/core"
	"github.com/bacnx/squad/internal/driver"
	"github.com/bacnx/squad/internal/driver/local"
	"github.com/bacnx/squad/internal/proto"
)

// Dir is squad's home: $SQUAD_HOME, else ~/.squad.
func Dir() string {
	if d := os.Getenv("SQUAD_HOME"); d != "" {
		return d
	}
	h, err := os.UserHomeDir()
	if err != nil {
		h = "."
	}
	return filepath.Join(h, ".squad")
}

// DaemonFile is daemon.json in dir: the running daemon's address and admin token.
func DaemonFile(dir string) string { return filepath.Join(dir, "daemon.json") }

// ServeLog is where the daemon writes its problems.
func ServeLog(dir string) string { return filepath.Join(dir, "serve.log") }

// Serve runs the daemon until a signal or `squad stop`.
func Serve(version, self string) error {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	unlock, err := lock(dir)
	if err != nil {
		return err
	}
	defer unlock()
	logf, err := os.OpenFile(ServeLog(dir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		log.SetOutput(logf)
		defer logf.Close()
	}
	if err := local.EnsureProfiles(dir); err != nil {
		log.Printf("profiles: %v", err)
		return err
	}
	if err := EnsureConfig(dir); err != nil {
		log.Printf("config: %v", err)
		return err
	}
	set, err := LoadConfig(dir)
	if err != nil {
		log.Printf("config: %v", err)
		return err
	}
	eng, err := core.Open(core.Options{Dir: dir, Version: version, Runtimes: []driver.Runtime{local.NewClaude(dir, self)},
		DefaultHarness: set.Harness, AllowedRoots: set.AllowedRoots,
		Prompts: PromptsFor(dir, set.Prompts, func(msg string) { log.Print(msg) })})
	if err != nil {
		log.Printf("open: %v", err)
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		eng.Close()
		return err
	}
	admin := randomToken()
	info := proto.Info{Addr: ln.Addr().String(), Pid: os.Getpid(), Token: admin, Version: version}
	b, _ := json.MarshalIndent(info, "", "  ")
	if err := os.WriteFile(DaemonFile(dir), b, 0o600); err != nil {
		eng.Close()
		return err
	}
	defer os.Remove(DaemonFile(dir))
	log.Printf("squad %s serving on %s (pid %d)", version, info.Addr, info.Pid)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s := &srv{eng: eng, admin: admin, stop: stop, dir: dir, set: set}
	httpSrv := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				eng.Tick()
			}
		}
	}()
	go s.gcLoop(ctx)
	go func() { _ = httpSrv.Serve(ln) }()
	<-ctx.Done()
	log.Printf("stopping")
	sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(sctx)
	eng.Close()
	return nil
}

type srv struct {
	eng   *core.Engine
	admin string
	stop  context.CancelFunc
	dir   string
	set   Settings
}

// ArchiveDir is where gc writes its archives.
func ArchiveDir(dir string) string { return filepath.Join(dir, "archive") }

// gcLoop runs gc at the start and every 24 hours while gc.closed_after is on.
func (s *srv) gcLoop(ctx context.Context) {
	if s.set.GCClosedAfter == 0 {
		return
	}
	for {
		res, err := s.gc("", false)
		if err != nil {
			log.Printf("gc: %v", err)
		}
		for _, u := range res.Units {
			if u.Skipped != "" {
				log.Printf("gc: %s %s kept: %s", u.Kind, u.Name, u.Skipped)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}

// gc collects the closed teams and gone solos older than before (a retention, "" for
// gc.closed_after).
func (s *srv) gc(before string, dry bool) (core.GCResult, error) {
	age := s.set.GCClosedAfter
	if before != "" {
		var err error
		if age, err = ParseRetention(before); err != nil {
			return core.GCResult{}, &core.Error{Code: "invalid", Msg: "--closed-before " + err.Error()}
		}
	}
	if age == 0 {
		return core.GCResult{}, &core.Error{Code: "invalid", Msg: "gc is off (gc.closed_after in config.yaml); name an age: squad gc --closed-before 14d"}
	}
	return s.eng.GC(core.GCArgs{ClosedBefore: time.Now().Add(-age), DryRun: dry, ArchiveDir: ArchiveDir(s.dir), ArchiveKeep: s.set.GCArchiveKeep})
}

func (s *srv) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/rpc" {
		http.NotFound(w, r)
		return
	}
	var req proto.Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		writeJSON(w, proto.Response{Code: "invalid", Error: "bad request: " + err.Error()})
		return
	}
	res, err := s.call(r.Context(), req)
	if err != nil {
		var ce *core.Error
		if errors.As(err, &ce) {
			writeJSON(w, proto.Response{Code: ce.Code, Rule: ce.Rule, Error: ce.Msg})
		} else {
			log.Printf("%s: %v", req.Op, err)
			writeJSON(w, proto.Response{Code: "internal", Error: err.Error()})
		}
		return
	}
	b, err := json.Marshal(res)
	if err != nil {
		writeJSON(w, proto.Response{Code: "internal", Error: err.Error()})
		return
	}
	writeJSON(w, proto.Response{OK: true, Result: b})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *srv) call(ctx context.Context, req proto.Request) (any, error) {
	if !proto.ParticipantVerbs[req.Op] && req.Token != s.admin {
		return nil, &core.Error{Code: "auth", Msg: "admin token required"}
	}
	a, err := decode[proto.Args](req.Args)
	if err != nil {
		return nil, err
	}
	wait := time.Duration(min(max(a.Wait, 0), 600)) * time.Second // inbox, wake_wait
	switch req.Op {
	case proto.VerbPing:
		return "pong", nil
	case proto.VerbRegister:
		return s.eng.RegisterSession(a.Cwd, a.Model, a.Key, a.Harness)
	case proto.VerbWhoami:
		return s.eng.Whoami(req.Token)
	case proto.VerbHeartbeat:
		return s.eng.Heartbeat(req.Token)
	case proto.VerbUnregister:
		return nil, s.eng.Unregister(req.Token)
	case proto.VerbSend:
		sa, err := decode[core.SendArgs](req.Args)
		if err != nil {
			return nil, err
		}
		r, err := s.eng.Send(req.Token, sa)
		if err != nil {
			return nil, err
		}
		return r.Text(), nil
	case proto.VerbRelease:
		return s.eng.Release(a.Name)
	case proto.VerbWhy:
		lines, verdict, err := s.eng.Why(a.Name, a.To)
		if err != nil {
			return nil, err
		}
		return map[string]any{"checks": lines, "verdict": verdict}, nil
	case proto.VerbLog:
		return s.eng.Log(a.After, a.Team, a.Limit)
	case proto.VerbDoctor:
		return s.eng.Doctor()
	case proto.VerbInbox:
		switch a.View {
		case "":
		case "board":
			return s.eng.BoardText(req.Token)
		default:
			return nil, &core.Error{Code: "invalid", Msg: fmt.Sprintf("unknown inbox view %q (board)", a.View)}
		}
		msgs, err := s.eng.Inbox(ctx, req.Token, wait)
		if err != nil {
			return nil, err
		}
		if len(msgs) == 0 {
			return "no new mail", nil
		}
		return core.FormatMail(msgs), nil
	case proto.VerbWakeWait:
		return s.eng.WaitWake(ctx, req.Token, wait)
	case proto.VerbHarnessEvent:
		ha, err := decode[core.HarnessEventArgs](req.Args)
		if err != nil {
			return nil, err
		}
		return s.eng.HarnessEvent(ha)
	case proto.VerbWho:
		return s.eng.Who(req.Token)
	case proto.VerbAgent:
		aa, err := decode[core.AgentArgs](req.Args)
		if err != nil {
			return nil, err
		}
		return s.eng.Agent(req.Token, aa)
	case proto.VerbView:
		return s.eng.View(os.Getpid())
	case proto.VerbResolve:
		p, err := s.eng.Resolve(a.Name)
		if err != nil {
			return nil, err
		}
		return map[string]string{"id": p.ID, "name": p.Name, "kind": p.Kind}, nil
	case proto.VerbKill:
		return s.eng.Kill(a.Name)
	case proto.VerbInterrupt:
		return s.eng.Interrupt(a.Name)
	case proto.VerbResume:
		return s.eng.Resume(a.Name)
	case proto.VerbTeamDown:
		return s.eng.TeamDown(a.Name)
	case proto.VerbModel:
		return s.eng.SetModel(a.Name, a.Model, a.Thinking)
	case proto.VerbGC:
		return s.gc(a.Before, a.DryRun)
	case proto.VerbShutdown:
		go func() { time.Sleep(100 * time.Millisecond); s.stop() }()
		return "stopping", nil
	default:
		return nil, &core.Error{Code: "invalid", Msg: fmt.Sprintf("unknown op %q", req.Op)}
	}
}

func randomToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// LockFile holds the pid of the daemon that runs; it goes when that daemon is done.
func LockFile(dir string) string { return filepath.Join(dir, "squad.lock") }

// lock makes sure one daemon runs per home: squad.lock holds the pid of the one that runs.
func lock(dir string) (func(), error) {
	path := LockFile(dir)
	for range 2 {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		b, rerr := os.ReadFile(path)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if rerr == nil && local.Alive(pid) { // held, also when by this very process
			return nil, fmt.Errorf("a squad daemon already runs (pid %d)", pid)
		}
		os.Remove(path) // stale
	}
	return nil, errors.New("could not take " + path)
}

// decode reads a verb's arguments (none: the zero value).
func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &v); err != nil {
			return v, &core.Error{Code: "invalid", Msg: "bad args: " + err.Error()}
		}
	}
	return v, nil
}
