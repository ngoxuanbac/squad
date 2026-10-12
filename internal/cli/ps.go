package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bacnx/squad/internal/core"
	"github.com/bacnx/squad/internal/view"
)

// ps, top: teams and sessions as text (internal/view has the document itself).

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printPS(out io.Writer, v view.View) {
	fmt.Fprintf(out, "squad %s (pid %d): %d teams, %d sessions, %d workers (%d working)\n",
		v.Daemon.Version, v.Daemon.Pid, v.Summary.Teams, v.Summary.Sessions, v.Summary.Workers, v.Summary.Working)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	member := func(m view.Member, gate string) {
		tag := ""
		if m.Name == gate {
			tag = " (gate)"
		}
		task := m.Task
		if len(task) > 60 {
			task = task[:60] + "…"
		}
		unread := ""
		if m.Unread > 0 {
			unread = fmt.Sprintf("%d unread", m.Unread)
		}
		fmt.Fprintf(w, "  %s%s\t%s\t%s\t%s\t%s\t%s\t%s\n", m.Name, tag, m.Role, m.Kind, m.Status, m.Model, unread, task)
	}
	var closed []view.Team
	for _, t := range v.Teams {
		if t.Status != core.TeamOpen {
			closed = append(closed, t)
			continue
		}
		extra := ""
		if t.Parent != "" {
			extra = ", taskforce of " + t.Parent
		}
		fmt.Fprintf(w, "team %s [%s%s] %s\t\t\t\t\t\t\n", t.Name, t.Template, extra, t.Root)
		for _, m := range t.Members {
			member(m, t.Gate)
		}
	}
	if len(v.Solos) > 0 {
		fmt.Fprintln(w, "solo sessions\t\t\t\t\t\t")
		for _, s := range v.Solos {
			member(s, "")
		}
	}
	w.Flush()
	if len(closed) > 0 {
		names := make([]string, 0, len(closed))
		for _, t := range closed {
			names = append(names, t.Name)
		}
		fmt.Fprintf(out, "closed: %s\n", strings.Join(names, ", "))
	}
	if n := len(v.Events); n > 0 {
		fmt.Fprintln(out, "\nlatest events:")
		for _, e := range v.Events[max(0, n-8):] {
			fmt.Fprintf(out, "  %s %-6s %s\n", localTime(e.At, "15:04:05"), e.Kind, e.Text)
		}
	}
}

func printTail(lines []view.TailLine) {
	for _, l := range lines {
		label := map[string]string{"mail": "✉ mail", "user": "› user", "assistant": "● claude", "thinking": "∴ think",
			"tool": "⚙ tool", "tool_result": "  ↳", "result": "✓ done", "system": "· system", "error": "✗ error"}[l.Kind]
		text := l.Text
		if l.Rest != "" {
			text += "  " + l.Rest
		}
		fmt.Printf("%-9s %s\n", label, strings.ReplaceAll(text, "\n", "\n          "))
	}
}

// localTime is an RFC 3339 time in layout, local; as it is when it does not parse.
func localTime(s, layout string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Local().Format(layout)
	}
	return s
}
