// Package cli is the squad command line.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bacnx/squad/internal/core"
	"github.com/bacnx/squad/internal/proto"
	"github.com/bacnx/squad/internal/server"
	"github.com/bacnx/squad/internal/view"
	"github.com/bacnx/squad/manifests"
)

// Version is set at build time (-ldflags "-X github.com/bacnx/squad/internal/cli.Version=v0.1.0").
var Version = "0.1.0-dev"

const usage = `squad: teams of Claude Code agents, with Paseo as the window onto them.

Usage:
  squad setup [claude|paseo]      add squad to Claude Code (MCP) or to Paseo (plugin); alone: status
  squad setup remove claude|paseo take squad out of Claude Code or Paseo
  squad ps [--json|--view]        teams, members and solo sessions
  squad top                       ps, refreshed every 2 seconds
  squad tail <name> [-n N] [-f]   a worker's log, readable (--view, --json for tools)
  squad kill|x <name>             stop a worker's process tree (new mail resumes it)
  squad abort <name>              cancel a worker's current turn
  squad resume <name>             start a stopped worker again in its session
  squad team down <team>          close a team: workers stopped
  squad why <from> <to>           every check a send from <from> to <to> passes or fails; sends nothing
  squad log [--after N] [--team T] [--limit N]   decisions and lifecycle events
  squad doctor                    what looks stuck (exit 1 when anything does)
  squad release #N                deliver a message held by a limit
  squad model <worker> [model] [--thinking L]   switch a worker's model or thinking level
  squad gc [--closed-before 14d] [--dry-run]    archive and delete old closed teams and gone solos
  squad archive list | show <file>              the archives gc wrote
  squad check                     read config.yaml, the Claude profile and your templates (exit 1 on errors)
  squad template list             the templates teams are founded from
  squad template new <name> --from <built-in>
  squad stop                      stop the daemon (and every worker)
  squad restart                   stop and start the daemon (config.yaml is read at its start)
  squad serve                     run the daemon in the foreground (commands start it as needed)
  squad mcp                       the MCP server Claude Code runs (setup claude adds it)
  squad version

Every command but serve and mcp takes --no-start: fail instead of starting the daemon.
`

// Main runs the command line and returns the exit code.
func Main(args []string) int {
	if err := run(args); err != nil {
		if !errors.Is(err, errQuiet) {
			fmt.Fprintln(os.Stderr, "squad:", err)
		}
		return 1
	}
	return 0
}

func self() string {
	exe, err := os.Executable()
	if err != nil {
		return "squad"
	}
	return exe
}

// parse parses flags wherever they are among the arguments (`tail w1 -n 5 --view`), and returns
// the positional ones.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// connect makes sure the daemon answers: started unless noStart.
func connect(noStart bool) error {
	if noStart {
		return pingDaemon(2 * time.Second)
	}
	return ensureDaemon(self())
}

// pingDaemon is nil when the daemon answers within d.
func pingDaemon(d time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return call(ctx, proto.VerbPing, "", nil, nil)
}

// errQuiet exits 1 after the command printed why.
var errQuiet = errors.New("exit 1, already said why")

// say runs an admin verb that answers with a line, and prints it.
func say(op string, args any) error {
	var out string
	if err := adminCall(op, args, &out); err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

func adminCall(op string, args, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return call(ctx, op, "", args, out)
}

// topFrames is how many frames top draws (0: until stopped); topEvery the pause between them.
var (
	topFrames = 0
	topEvery  = 2 * time.Second
)

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	noStart := fs.Bool("no-start", false, "do not start the daemon")
	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	case "version", "--version":
		fmt.Println("squad", Version)
		return nil
	case "serve":
		return server.Serve(Version, self())
	case "mcp":
		return runMCP(self(), Version, os.Stdin, os.Stdout, os.Getenv)
	case "hook":
		if len(rest) != 2 {
			return errors.New("usage: squad hook claude <HookEvent>")
		}
		return runHook(rest[0], rest[1], os.Stdin, os.Stdout, os.Getenv)
	case "stop":
		if _, err := parse(fs, rest); err != nil {
			return err
		}
		if err := adminCall("shutdown", nil, nil); err != nil {
			if errors.Is(err, errDown) {
				fmt.Println("the squad daemon is not running")
				return nil
			}
			return err
		}
		fmt.Println("stopping the squad daemon")
		return nil
	case "ps":
		asJSON := fs.Bool("json", false, "print JSON")
		asView := fs.Bool("view", false, "print the view document (internal/view)")
		if _, err := parse(fs, rest); err != nil {
			return err
		}
		if err := connect(*noStart); err != nil {
			return err
		}
		var v view.View
		if err := adminCall("view", nil, &v); err != nil {
			return err
		}
		if *asJSON || *asView {
			return printJSON(v)
		}
		printPS(os.Stdout, v)
		return nil
	case "top":
		if _, err := parse(fs, rest); err != nil {
			return err
		}
		if err := connect(*noStart); err != nil {
			return err
		}
		for i := 0; topFrames == 0 || i < topFrames; i++ {
			if i > 0 {
				time.Sleep(topEvery)
			}
			var v view.View
			if err := adminCall("view", nil, &v); err != nil {
				return err
			}
			var b strings.Builder
			printPS(&b, v)
			fmt.Print("\x1b[H\x1b[2J" + b.String() + "\n(ctrl+c to quit)\n")
		}
		return nil
	case "tail":
		n := fs.Int("n", 40, "lines")
		follow := fs.Bool("f", false, "follow")
		asView := fs.Bool("view", false, "print the tail document (internal/view)")
		asJSON := fs.Bool("json", false, "print the raw stream-json lines")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return errors.New("usage: squad tail <name> [-n N] [-f]")
		}
		if err := connect(*noStart); err != nil {
			return err
		}
		var p struct{ ID string }
		if err := adminCall("resolve", map[string]string{"name": pos[0]}, &p); err != nil {
			return err
		}
		return tail(p.ID, *n, *follow, *asView, *asJSON)
	case "kill", "x", "abort", "resume":
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return fmt.Errorf("usage: squad %s <name>", cmd)
		}
		if err := connect(*noStart); err != nil {
			return err
		}
		op := map[string]string{"kill": "kill", "x": "kill", "abort": "interrupt", "resume": "resume"}[cmd]
		return say(op, proto.Args{Name: pos[0]})
	case "why", "log", "doctor", "release":
		return diag(cmd, fs, rest, noStart)
	case "team":
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) != 2 || pos[0] != "down" {
			return errors.New("usage: squad team down <team>")
		}
		if err := connect(*noStart); err != nil {
			return err
		}
		return say(proto.VerbTeamDown, proto.Args{Name: pos[1]})
	case "template":
		from := fs.String("from", "", "the built-in template to copy")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		switch {
		case len(pos) == 1 && pos[0] == "list":
			list, err := manifests.List(core.TemplatesDir(server.Dir()))
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tORIGIN\tTASKFORCE\tSUMMARY")
			for _, t := range list {
				tf := ""
				if t.Taskforce {
					tf = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.Name, t.Origin, tf, t.Summary)
			}
			return w.Flush()
		case len(pos) == 2 && pos[0] == "new" && *from != "":
			path, err := manifests.CopyBuiltin(core.TemplatesDir(server.Dir()), *from, pos[1])
			if err != nil {
				return err
			}
			fmt.Println("wrote", path)
			return nil
		default:
			return errors.New("usage: squad template list | squad template new <name> --from <built-in>")
		}
	case "gc":
		return gcCmd(fs, rest, noStart)
	case "archive":
		return archiveCmd(fs, rest)
	case "model":
		return modelCmd(fs, rest, noStart)
	case "check":
		if pos, err := parse(fs, rest); err != nil || len(pos) != 0 {
			return errors.New("usage: squad check")
		}
		return check()
	case "restart":
		if pos, err := parse(fs, rest); err != nil || len(pos) != 0 {
			return errors.New("usage: squad restart")
		}
		return restart()
	case "setup":
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		return setup(pos)
	default:
		return fmt.Errorf("unknown command %q (squad help)", cmd)
	}
}
