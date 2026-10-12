package core

import (
	"fmt"
	"strings"
	"time"
)

// EngineID is the sender of the engine's own mail (notices, timer fires, hold and respawn notes).
// It is never an address.
const EngineID = "engine"

// Reserved addresses: no participant or team may take these names.
const (
	AddrBoard  = "board"  // the team's pins
	AddrNotify = "notify" // the engine's channel for notices
)

// Msg is one message as a reader gets it.
type Msg struct {
	ID          int64  `json:"id"`             // its #N
	FromID      string `json:"-"`              // participant id, or EngineID
	From        string `json:"from"`           // the sender's name
	Label       string `json:"label"`          // the sender as the reader sees it
	Kind        string `json:"kind,omitempty"` // the sender's free label
	ReplyTo     int64  `json:"reply_to,omitempty"`
	CcOf        int64  `json:"cc_of,omitempty"`         // a routing cc copy of #CcOf
	CcOfTo      string `json:"cc_of_mail_to,omitempty"` // the original's recipient, as the reader sees it
	Body        string `json:"body"`
	At          string `json:"at"`
	Redelivered bool   `json:"redelivered,omitempty"`
}

// label is the sender fromID as reader sees it (reader nil: as an outsider).
//   - the engine: engine;
//   - a solo: name (solo);
//   - in another team than the reader's: name (role, team T);
//   - the one the reader reports to: name (role, you report to them);
//   - one that reports to the reader: name (role, reports to you);
//   - else name (role).
func (e *Engine) label(reader *Participant, fromID string) (name, label string) {
	if fromID == EngineID {
		return EngineID, EngineID
	}
	s, _ := e.byID(fromID)
	if s == nil {
		return fromID, fromID + " (solo)"
	}
	switch {
	case s.TeamID == "":
		return s.Name, s.Name + " (solo)"
	case reader == nil || s.TeamID != reader.TeamID:
		return s.Name, fmt.Sprintf("%s (%s, team %s)", s.Name, s.Role, e.teamName(s.TeamID))
	case reader.SpawnedBy == s.ID:
		return s.Name, fmt.Sprintf("%s (%s, you report to them)", s.Name, s.Role)
	case s.SpawnedBy == reader.ID:
		return s.Name, fmt.Sprintf("%s (%s, reports to you)", s.Name, s.Role)
	default:
		return s.Name, fmt.Sprintf("%s (%s)", s.Name, s.Role)
	}
}

// mailCols are the columns readMail scans.
const mailCols = `id, from_id, body, created_at, kind, reply_to, cc_of, gives`

// readMail reads the mail rows of where (over mail m) for reader, oldest first.
func (e *Engine) readMail(reader *Participant, where string, args ...any) ([]Msg, error) {
	rows, err := e.db.Query(`SELECT `+mailCols+` FROM mail m WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	type row struct {
		m     Msg
		gives int
	}
	var rs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.m.ID, &r.m.FromID, &r.m.Body, &r.m.At, &r.m.Kind, &r.m.ReplyTo, &r.m.CcOf, &r.gives); err != nil {
			rows.Close()
			return nil, err
		}
		rs = append(rs, r)
	}
	rows.Close()
	out := make([]Msg, 0, len(rs))
	for _, r := range rs {
		m := r.m
		m.From, m.Label = e.label(reader, m.FromID)
		m.Redelivered = r.gives > 0
		if m.CcOf != 0 {
			var origTo string
			if e.db.QueryRow(`SELECT to_id FROM mail WHERE id=?`, m.CcOf).Scan(&origTo) == nil {
				_, m.CcOfTo = e.label(reader, origTo)
			}
		}
		out = append(out, m)
	}
	return out, nil
}

// FormatMail is how mail reads to a model: one block per message, the newest last.
func FormatMail(msgs []Msg) string {
	return formatMail(msgs, fmt.Sprintf("%d new message%s", len(msgs), plural(len(msgs))), time.Now())
}

func formatMail(msgs []Msg, heading string, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[squad] %s:\n", heading)
	for _, m := range msgs {
		fmt.Fprintf(&b, "\n<message id=\"#%d\" from=%q sender=%q", m.ID, m.Label, m.From)
		if m.Kind != "" {
			fmt.Fprintf(&b, " kind=%q", m.Kind)
		}
		if m.ReplyTo != 0 {
			fmt.Fprintf(&b, " reply_to=\"#%d\"", m.ReplyTo)
		}
		if m.CcOf != 0 {
			fmt.Fprintf(&b, " cc_of=\"#%d\" cc_of_mail_to=%q", m.CcOf, m.CcOfTo)
		}
		at, age := clock(m.At, now)
		fmt.Fprintf(&b, " at=%q", at)
		if age != "" {
			fmt.Fprintf(&b, " age=%q", age)
		}
		if m.Redelivered {
			b.WriteString(` redelivered="true"`)
		}
		fmt.Fprintf(&b, ">\n%s\n</message>\n", m.Body)
	}
	b.WriteString("\nTo reply, use the send tool with to=<sender> and reply_to=<its #N>.")
	return b.String()
}

// clock is a mail's time as the model reads it (15:04:05 today, else 2006-01-02 15:04), and its
// age when older than a minute.
func clock(at string, now time.Time) (string, string) {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return at, ""
	}
	t = t.In(now.Location())
	s := t.Format("2006-01-02 15:04")
	if y1, m1, d1 := t.Date(); true {
		if y2, m2, d2 := now.Date(); y1 == y2 && m1 == m2 && d1 == d2 {
			s = t.Format("15:04:05")
		}
	}
	return s, age(now.Sub(t))
}

// age is d as 4m, 2h, 2h5m, 3d, 3d2h; "" under a minute.
func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return ""
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		h, m := int(d/time.Hour), int(d%time.Hour/time.Minute)
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	default:
		days, h := int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour)
		if h == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd%dh", days, h)
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// title is a mail body's first non-empty line, as ps shows an assignment: markdown emphasis and
// leading markers dropped, cut to 100 runes.
func title(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.NewReplacer("**", "", "__", "", "`", "").Replace(line)
		line = strings.TrimLeft(line, "#>-*+ \t")
		if line = strings.TrimSpace(line); line != "" {
			if r := []rune(line); len(r) > 100 {
				return string(r[:100]) + "…"
			}
			return line
		}
	}
	return ""
}
