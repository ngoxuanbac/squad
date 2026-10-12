package cli

import (
	"errors"
	"flag"
	"fmt"

	"github.com/bacnx/squad/internal/core"
	"github.com/bacnx/squad/internal/proto"
)

// diag runs why, log, doctor and release.
func diag(cmd string, fs *flag.FlagSet, rest []string, noStart *bool) error {
	after := fs.Int64("after", 0, "events after this seq")
	team := fs.String("team", "", "only this team's events")
	limit := fs.Int("limit", 0, "at most this many events")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, rest)
	if err != nil {
		return err
	}
	if err := connect(*noStart); err != nil {
		return err
	}
	switch cmd {
	case "why":
		if len(pos) != 2 {
			return errors.New("usage: squad why <from> <to>")
		}
		var r struct {
			Checks  []core.WhyLine `json:"checks"`
			Verdict string         `json:"verdict"`
		}
		if err := adminCall(proto.VerbWhy, proto.Args{Name: pos[0], To: pos[1]}, &r); err != nil {
			return err
		}
		if *asJSON {
			return printJSON(r)
		}
		var rule string
		for _, c := range r.Checks {
			tag := ""
			if c.Rule != "" {
				tag = " [" + c.Rule + "]"
			}
			fmt.Printf("%-10s %-5s %s%s\n", c.Check, c.Result, c.Detail, tag)
			if c.Result == "deny" || c.Result == "hold" {
				rule = c.Rule
			}
		}
		if rule != "" {
			fmt.Printf("verdict: %s rule=%s\n", r.Verdict, rule)
		} else {
			fmt.Println("verdict:", r.Verdict)
		}
		return nil
	case "log":
		var evs []core.LogEvent
		if err := adminCall(proto.VerbLog, proto.Args{After: *after, Team: *team, Limit: *limit}, &evs); err != nil {
			return err
		}
		if *asJSON {
			return printJSON(evs)
		}
		for _, ev := range evs {
			fmt.Printf("%5d %s %-10s %-12s %s\n", ev.Seq, localTime(ev.At, "01-02 15:04:05"), ev.Kind, ev.Team, ev.Text)
		}
		return nil
	case "doctor":
		var fs []core.Finding
		if err := adminCall(proto.VerbDoctor, nil, &fs); err != nil {
			return err
		}
		if *asJSON {
			if err := printJSON(fs); err != nil {
				return err
			}
		} else if len(fs) == 0 {
			fmt.Println("no findings")
		}
		for _, f := range fs {
			if !*asJSON {
				fmt.Printf("%-14s %-16s %s\n", f.Kind, f.Who, f.Detail)
			}
		}
		if len(fs) > 0 {
			return errQuiet
		}
		return nil
	default: // release
		if len(pos) != 1 {
			return errors.New("usage: squad release #N")
		}
		return say(proto.VerbRelease, proto.Args{Name: pos[0]})
	}
}
