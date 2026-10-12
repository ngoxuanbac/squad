package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gc archives a closed team (its members, mail and the worker's log go) and a gone solo, reads
// each archive back, and only then deletes them; a dry run changes nothing.
func TestGCArchivesThenDeletes(t *testing.T) {
	e := newEnv(t)
	lead := leadWithPeer(t, e)
	lead.mail("done")
	lead.ok(do("agent", map[string]any{"action": "close"}))
	e.waitStatus("p1", "stopped")
	e.session(t.TempDir()).quit()
	time.Sleep(2100 * time.Millisecond) // older than --closed-before 1s, at a second's resolution

	var dry, res struct {
		Units []struct{ Kind, Name, Archive, Skipped string }
	}
	must(t, json.Unmarshal([]byte(e.squad("gc", "--closed-before", "1s", "--dry-run", "--json")), &dry))
	if len(dry.Units) != 2 || e.member("p1").Status == "" {
		t.Fatalf("dry run: %+v, p1 %+v", dry, e.member("p1"))
	}
	if files, _ := filepath.Glob(filepath.Join(e.home, "archive", "*")); len(files) != 0 {
		t.Fatalf("a dry run wrote %v", files)
	}

	must(t, json.Unmarshal([]byte(e.squad("gc", "--closed-before", "1s", "--json")), &res))
	if len(res.Units) != 2 {
		t.Fatalf("gc: %+v", res)
	}
	if v := e.ps(); len(v.Teams) != 0 || e.member("p1").Status != "" {
		t.Fatalf("after gc: %+v", v)
	}
	for _, left := range []string{"logs", "run"} { // p1's log and run scratch
		if files, _ := filepath.Glob(filepath.Join(e.home, left, "*")); len(files) != 0 {
			t.Fatalf("outlived gc: %v", files)
		}
	}
	for _, u := range res.Units {
		if u.Skipped != "" || u.Archive == "" {
			t.Fatalf("unit not archived: %+v", u)
		}
		if _, err := os.Stat(filepath.Join(e.home, "archive", u.Archive)); err != nil { // named in ~/.squad/archive
			t.Fatal(err)
		}
		if !strings.Contains(e.squad("archive", "list"), u.Archive) {
			t.Fatalf("archive list lacks %s", u.Archive)
		}
		if u.Kind == "team" {
			if show := e.squad("archive", "show", u.Archive); !strings.Contains(show, "p1") || !strings.Contains(show, "done") {
				t.Fatalf("archive show:\n%s", show)
			}
		}
	}
}
