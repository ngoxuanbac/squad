// Tail lines: what `squad tail` and the Paseo plugin show of a worker log.

package view

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// TailVersion is the version of the tail --view document.
const TailVersion = 1

// TailLine is one readable line of a worker's log.
type TailLine struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	Rest string `json:"rest,omitempty"`
}

// ReadTail reads a worker log and turns its stream-json lines into readable lines; the last n.
func ReadTail(path string, n int) ([]TailLine, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return []TailLine{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []TailLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	for sc.Scan() {
		out = append(out, ParseLine(sc.Bytes())...)
		if len(out) > 4*n+200 {
			out = out[len(out)-n:]
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	if out == nil {
		out = []TailLine{}
	}
	return out, sc.Err()
}

func cut(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func flat(s string, n int) string { return cut(strings.Join(strings.Fields(s), " "), n) }

// ParseLine turns one stream-json line of a worker log into readable lines.
func ParseLine(b []byte) []TailLine {
	var m struct {
		Type    string          `json:"type"`
		Subtype string          `json:"subtype"`
		Text    string          `json:"text"`
		Model   string          `json:"model"`
		Message json.RawMessage `json:"message"`
		Result  string          `json:"result"`
		IsError bool            `json:"is_error"`
		Dur     float64         `json:"duration_ms"`
	}
	if json.Unmarshal(b, &m) != nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return []TailLine{{Kind: "error", Text: cut(s, 300)}}
		}
		return nil
	}
	switch m.Type {
	case "squad_mail":
		return []TailLine{{Kind: "mail", Text: cut(m.Text, 2000)}}
	case "system":
		switch m.Subtype {
		case "init":
			return []TailLine{{Kind: "system", Text: "session started", Rest: m.Model}}
		case "api_retry":
			var r struct {
				Attempt int    `json:"attempt"`
				Max     int    `json:"max_retries"`
				Error   string `json:"error"`
				Status  *int   `json:"error_status"`
			}
			_ = json.Unmarshal(b, &r)
			why := r.Error
			if r.Status != nil {
				why = fmt.Sprintf("%s (HTTP %d)", why, *r.Status)
			}
			return []TailLine{{Kind: "error", Text: fmt.Sprintf("API retry %d/%d", r.Attempt, r.Max), Rest: why}}
		}
		return nil
	case "result":
		if m.IsError {
			return []TailLine{{Kind: "error", Text: "turn failed", Rest: cut(m.Result, 300)}}
		}
		return []TailLine{{Kind: "result", Text: fmt.Sprintf("turn done (%.1fs)", m.Dur/1000), Rest: cut(m.Result, 300)}}
	case "assistant", "user":
		var msg struct {
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(m.Message, &msg) != nil {
			return nil
		}
		var s string
		if json.Unmarshal(msg.Content, &s) == nil {
			if m.Type == "user" {
				return []TailLine{{Kind: "user", Text: cut(s, 2000)}}
			}
			return []TailLine{{Kind: "assistant", Text: cut(s, 2000)}}
		}
		var blocks []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			Thinking string          `json:"thinking"`
			Name     string          `json:"name"`
			Input    map[string]any  `json:"input"`
			Content  json.RawMessage `json:"content"`
			IsError  bool            `json:"is_error"`
		}
		if json.Unmarshal(msg.Content, &blocks) != nil {
			return nil
		}
		var out []TailLine
		for _, bl := range blocks {
			switch bl.Type {
			case "text":
				if m.Type == "user" {
					if !strings.HasPrefix(bl.Text, "[squad]") { // mail is logged as squad_mail already
						out = append(out, TailLine{Kind: "user", Text: cut(bl.Text, 2000)})
					}
				} else if strings.TrimSpace(bl.Text) != "" {
					out = append(out, TailLine{Kind: "assistant", Text: cut(bl.Text, 2000)})
				}
			case "thinking":
				if strings.TrimSpace(bl.Thinking) != "" {
					out = append(out, TailLine{Kind: "thinking", Text: flat(bl.Thinking, 300)})
				}
			case "tool_use":
				out = append(out, TailLine{Kind: "tool", Text: bl.Name, Rest: toolInput(bl.Input)})
			case "tool_result":
				text := resultText(bl.Content)
				kind := "tool_result"
				if bl.IsError {
					kind = "error"
				}
				out = append(out, TailLine{Kind: kind, Text: flat(text, 200)})
			}
		}
		return out
	}
	return nil
}

func toolInput(in map[string]any) string {
	for _, k := range []string{"command", "file_path", "path", "pattern", "url", "query", "to", "action", "description"} {
		if v, ok := in[k].(string); ok && v != "" {
			s := v
			if k == "to" {
				s = "to " + v
				if body, ok := in["body"].(string); ok {
					s += ": " + body
				}
			}
			if k == "action" {
				if t, ok := in["role"].(string); ok && t != "" {
					s += " role=" + t
				}
				if t, ok := in["template"].(string); ok && t != "" {
					s += " template=" + t
				}
			}
			return flat(s, 200)
		}
	}
	b, _ := json.Marshal(in)
	return flat(string(b), 200)
}

func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
			b.WriteString(" ")
		}
		return b.String()
	}
	return string(raw)
}
