package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bacnx/squad/internal/core"
	"github.com/bacnx/squad/internal/driver/local"
	"github.com/bacnx/squad/internal/proto"
	"github.com/bacnx/squad/internal/server"
	"github.com/bacnx/squad/manifests"
)

// Housekeeping: gc and its archives, a worker's model, check, restart.

func gcCmd(fs *flag.FlagSet, args []string, noStart *bool) error {
	before := fs.String("closed-before", "", "collect what closed (or went) longer ago than this: 14d, 36h (default gc.closed_after)")
	dry := fs.Bool("dry-run", false, "list what would go, change nothing")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return errors.New("usage: squad gc [--closed-before 14d] [--dry-run] [--json]")
	}
	if err := connect(*noStart); err != nil {
		return err
	}
	var res core.GCResult
	if err := adminCall(proto.VerbGC, proto.Args{Before: *before, DryRun: *dry}, &res); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(res)
	}
	if len(res.Units) == 0 {
		fmt.Println("nothing to collect")
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if len(res.Units) > 0 {
		fmt.Fprintln(w, "KIND\tNAME\tROWS\tARCHIVE")
	}
	for _, u := range res.Units {
		where := u.Archive
		switch {
		case *dry:
			where = "(dry run)"
		case u.Skipped != "":
			where = "kept: " + u.Skipped
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", u.Kind, u.Name, counts(u.Counts), where)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for _, a := range res.ExpiredArchives {
		fmt.Println("deleted expired archive", a)
	}
	if !*dry && len(res.Units) > 0 {
		fmt.Println("archives are in", server.ArchiveDir(server.Dir()), "(squad archive show <file>)")
	}
	return nil
}

func counts(c map[string]int) string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, c[k]))
	}
	return strings.Join(parts, ", ")
}

// archiveCmd reads an archive gc wrote; it needs no daemon.
func archiveCmd(fs *flag.FlagSet, args []string) error {
	asJSON := fs.Bool("json", false, "print the archive as it is")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	dir := server.ArchiveDir(server.Dir())
	switch {
	case len(pos) == 1 && pos[0] == "list":
		entries, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				fmt.Println(e.Name())
			}
		}
		return nil
	case len(pos) == 2 && pos[0] == "show":
	default:
		return errors.New("usage: squad archive list | squad archive show <file> [--json]")
	}
	path := pos[1]
	if _, err := os.Stat(path); err != nil && !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	a, err := core.ReadArchive(path)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(a)
	}
	fmt.Printf("%s %s, archived %s\n", a.Kind, a.Name, a.At)
	for _, t := range a.Tables["teams"] {
		fmt.Printf("team %v: template %v, root %v, created %v, closed %v\n", t["name"], t["template"], t["root"], t["created_at"], t["closed_at"])
	}
	names := map[any]any{}
	for id, n := range a.Names {
		names[id] = n
	}
	fmt.Println("\nmembers:")
	for _, p := range a.Tables["participants"] {
		names[p["id"]] = p["name"]
		fmt.Printf("  %v\t%v %v\t%v\n", p["name"], p["kind"], p["role"], p["task"])
	}
	fmt.Printf("\nmail (%d):\n", len(a.Tables["mail"]))
	for _, m := range a.Tables["mail"] {
		from, to := m["from_id"], m["to_id"]
		if n, ok := names[from]; ok {
			from = n
		}
		if n, ok := names[to]; ok {
			to = n
		}
		fmt.Printf("  #%v %v %v -> %v: %v\n", m["id"], m["created_at"], from, to, oneLine(fmt.Sprint(m["body"]), 120))
	}
	return nil
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func modelCmd(fs *flag.FlagSet, args []string, noStart *bool) error {
	thinking := fs.String("thinking", "", "the thinking (effort) level: low, medium, high, …")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 || (len(pos) == 1 && *thinking == "") {
		return errors.New("usage: squad model <worker> [model] [--thinking L]")
	}
	if err := connect(*noStart); err != nil {
		return err
	}
	a := proto.Args{Name: pos[0], Thinking: *thinking}
	if len(pos) == 2 {
		a.Model = pos[1]
	}
	return say(proto.VerbModel, a)
}

// check reads config.yaml, the harness profile, your templates and the files config.yaml names as
// the daemon will at its start. It needs no daemon.
func check() error {
	dir := server.Dir()
	var errs, warns []string
	set, err := server.LoadConfig(dir)
	if err != nil {
		errs = append(errs, err.Error())
	}
	// No config.yaml yet: the daemon writes it, and the rules file it names, at its start.
	if _, err := os.Stat(server.ConfigPath(dir)); errors.Is(err, os.ErrNotExist) {
		fmt.Println("no config.yaml yet: the daemon writes it with the defaults at its start")
		set.Prompts = nil
	}
	if set.Harness != "claude" {
		errs = append(errs, fmt.Sprintf("config.yaml: harness %q: squad runs claude", set.Harness))
	}
	if p, err := local.ReadClaudeProfile(dir); err != nil {
		errs = append(errs, err.Error())
	} else {
		if _, err := exec.LookPath(p.Cmd); err != nil {
			warns = append(warns, fmt.Sprintf("Claude profile: %s not found on PATH: workers will not start", p.Cmd))
		}
		if bad := badLevel(p.Thinking); bad != "" {
			errs = append(errs, "Claude profile: thinking "+bad)
		}
	}
	templates := map[string]bool{}
	list, err := manifests.List(core.TemplatesDir(dir))
	if err != nil {
		errs = append(errs, err.Error())
	}
	for _, t := range list {
		templates[t.Name] = true
		if t.Origin == "yours" && strings.HasPrefix(t.Summary, "error: ") {
			errs = append(errs, fmt.Sprintf("template %s: %s", t.Name, strings.TrimPrefix(t.Summary, "error: ")))
			continue
		}
		m, err := manifests.Load(core.TemplatesDir(dir), t.Name)
		if err != nil {
			continue
		}
		for name, r := range m.Roles {
			if h := r.Spawn.Harness; h != "" && h != local.ClaudeHarness {
				errs = append(errs, fmt.Sprintf("template %s: role %s: harness %q: squad runs claude", t.Name, name, h))
			} else if bad := badLevel(r.Spawn.Thinking); bad != "" {
				errs = append(errs, fmt.Sprintf("template %s: role %s: thinking %s", t.Name, name, bad))
			}
		}
	}
	for _, r := range set.AllowedRoots {
		if st, err := os.Stat(r); err != nil || !st.IsDir() {
			warns = append(warns, fmt.Sprintf("config.yaml: spawn.allowed_roots: %s is not a directory", r))
		}
	}
	for _, p := range set.Prompts {
		path := p.File
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		if _, err := os.ReadFile(path); err != nil {
			warns = append(warns, fmt.Sprintf("config.yaml: prompts: %s cannot be read: it is left out of role cards", p.File))
		}
		if len(p.Roles) == 0 {
			warns = append(warns, fmt.Sprintf("config.yaml: prompts: %s names no roles", p.File))
		}
		for _, r := range p.Roles {
			if tpl, _, ok := strings.Cut(r, "/"); ok && !templates[tpl] {
				warns = append(warns, fmt.Sprintf("config.yaml: prompts: %s: no template %q", p.File, tpl))
			}
		}
	}
	for _, e := range errs {
		fmt.Println("error:  ", e)
	}
	for _, w := range warns {
		fmt.Println("warning:", w)
	}
	fmt.Printf("checked %s: %d errors, %d warnings\n", dir, len(errs), len(warns))
	if len(errs) > 0 {
		return errQuiet
	}
	return nil
}

// badLevel says why Claude would not run thinking level v ("" when it would).
func badLevel(v string) string {
	if v = strings.TrimSpace(v); v == "" || v == local.Inherit || slices.Contains(local.ClaudeLevels, v) {
		return ""
	}
	return fmt.Sprintf("%q: Claude runs %s (or inherit)", v, strings.Join(local.ClaudeLevels, "|"))
}

// startDaemon starts the daemon for restart (a test runs one in its own process).
var startDaemon = func() error { return ensureDaemon(self()) }

// restart stops the daemon, waits for it to go, and starts it again: config.yaml and the harness
// profile are read afresh. Workers are stopped and resume with their next mail.
func restart() error {
	if _, err := server.LoadConfig(server.Dir()); err != nil {
		return fmt.Errorf("not restarted: %w (squad check)", err)
	}
	old, _ := daemonInfo()
	err := adminCall(proto.VerbShutdown, nil, nil)
	switch {
	case errors.Is(err, errDown):
		fmt.Println("the squad daemon was not running")
	case err != nil:
		return err
	default:
		deadline := time.Now().Add(15 * time.Second)
		for {
			err := pingDaemon(time.Second)
			// Down once it stopped answering and let go of its lock (its workers stopped).
			if _, lerr := os.Stat(server.LockFile(server.Dir())); errors.Is(err, errDown) && errors.Is(lerr, os.ErrNotExist) {
				break
			}
			// Or another daemon answers already: a client (squad mcp, Paseo) started one meanwhile.
			if now, ierr := daemonInfo(); err == nil && ierr == nil && now.Token != old.Token {
				printRestarted(now)
				return nil
			}
			if time.Now().After(deadline) {
				return errors.New("the squad daemon did not stop")
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if err := startDaemon(); err != nil {
		return err
	}
	info, _ := daemonInfo()
	printRestarted(info)
	return nil
}

func printRestarted(info proto.Info) {
	fmt.Printf("squad daemon restarted (pid %d, version %s)\n", info.Pid, info.Version)
}
