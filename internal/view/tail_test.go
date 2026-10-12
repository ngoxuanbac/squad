package view

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.jsonl")
	log := `{"type":"squad_mail","text":"[squad] from lead (lead, team api):\nfix it"}
{"type":"assistant","message":{"content":[{"type":"text","text":"On it."},{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"ok  pkg 0.1s"}]}}
{"type":"result","duration_ms":1500,"result":"done"}
not json from stderr
`
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := ReadTail(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{"mail", "assistant", "tool", "tool_result", "result", "error"}
	if len(lines) != len(kinds) {
		t.Fatalf("lines: %+v", lines)
	}
	for i, k := range kinds {
		if lines[i].Kind != k {
			t.Errorf("line %d: %s, want %s", i, lines[i].Kind, k)
		}
	}
	if lines[2].Rest != "go test ./..." {
		t.Errorf("tool rest: %q", lines[2].Rest)
	}
}

func TestParseLineKinds(t *testing.T) {
	cases := []struct {
		line string
		want []TailLine
	}{
		{`{"type":"system","subtype":"init","model":"haiku"}`, []TailLine{{Kind: "system", Text: "session started", Rest: "haiku"}}},
		{`{"type":"system","subtype":"api_retry","attempt":2,"max_retries":10,"error":"unknown","error_status":529}`,
			[]TailLine{{Kind: "error", Text: "API retry 2/10", Rest: "unknown (HTTP 529)"}}},
		{`{"type":"system","subtype":"api_retry","attempt":1,"max_retries":10,"error":"unknown","error_status":null}`,
			[]TailLine{{Kind: "error", Text: "API retry 1/10", Rest: "unknown"}}},
		{`{"type":"system","subtype":"other"}`, nil},
		{`{"type":"result","is_error":true,"result":"boom"}`, []TailLine{{Kind: "error", Text: "turn failed", Rest: "boom"}}},
		{`{"type":"user","message":{"content":"plain user text"}}`, []TailLine{{Kind: "user", Text: "plain user text"}}},
		{`{"type":"assistant","message":{"content":"plain answer"}}`, []TailLine{{Kind: "assistant", Text: "plain answer"}}},
		{`{"type":"user","message":{"content":[{"type":"text","text":"[squad] from x: hi"}]}}`, nil}, // mail is logged as squad_mail
		{`{"type":"user","message":{"content":[{"type":"text","text":"typed by hand"}]}}`, []TailLine{{Kind: "user", Text: "typed by hand"}}},
		{`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"  hmm\n  ok "},{"type":"text","text":"  "}]}}`,
			[]TailLine{{Kind: "thinking", Text: "hmm ok"}}},
		{`{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":[{"type":"text","text":"no"},{"type":"text","text":"such file"}]}]}}`,
			[]TailLine{{Kind: "error", Text: "no such file"}}},
		{`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__squad__send","input":{"to":"lead","body":"done"}}]}}`,
			[]TailLine{{Kind: "tool", Text: "mcp__squad__send", Rest: "to lead: done"}}},
		{`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__squad__agent","input":{"action":"spawn","role":"peer","template":"x"}}]}}`,
			[]TailLine{{Kind: "tool", Text: "mcp__squad__agent", Rest: "spawn role=peer template=x"}}},
		{`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Odd","input":{"n":1}}]}}`,
			[]TailLine{{Kind: "tool", Text: "Odd", Rest: `{"n":1}`}}},
		{`{"type":"assistant","message":"not an object"}`, nil},
		{`{"type":"assistant","message":{"content":42}}`, nil},
		{`{"type":"rate_limit_event"}`, nil},
		{`   `, nil},
	}
	for _, c := range cases {
		got := ParseLine([]byte(c.line))
		if len(got) != len(c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.line, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s:\n got %+v\nwant %+v", c.line, got[i], c.want[i])
			}
		}
	}
}

func TestReadTailMissingAndLong(t *testing.T) {
	if lines, err := ReadTail(filepath.Join(t.TempDir(), "none.jsonl"), 5); err != nil || len(lines) != 0 {
		t.Fatalf("missing log: %v %v", lines, err)
	}
	path := filepath.Join(t.TempDir(), "long.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 1000 {
		fmt.Fprintf(f, `{"type":"assistant","message":{"content":"line %d"}}`+"\n", i)
	}
	f.Close()
	lines, err := ReadTail(path, 3)
	if err != nil || len(lines) != 3 || lines[2].Text != "line 999" {
		t.Fatalf("last 3: %+v %v", lines, err)
	}
	long := strings.Repeat("é", 3000)
	if got := cut(long, 10); len([]rune(got)) != 11 || !strings.HasSuffix(got, "…") {
		t.Fatalf("cut: %q", got)
	}
}
