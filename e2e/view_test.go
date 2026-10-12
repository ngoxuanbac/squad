package e2e

import (
	"bytes"
	"encoding/json"
	"flag"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in e2e/testdata")

// The view documents are the contract with the Paseo plugin. A team with a worker and a
// taskforce, a solo and a closed team are drawn by `ps --view` and a worker's log by `tail --view`;
// with what changes from run to run made fixed, they must be the golden files, which the plugin's
// own test reads back field for field (extensions/paseo/test/contract.test.ts).
func TestViewContract(t *testing.T) {
	e := newEnv(t)
	tmp := t.TempDir()
	dir := func(name string) string {
		d := filepath.Join(tmp, name)
		must(t, os.Mkdir(d, 0o700))
		return d
	}
	old := e.founder(dir("old"), "p2p")
	old.ok(do("agent", map[string]any{"action": "close"}))

	lead := e.founder(dir("api"), "lead-peer")
	lead.ok(spawn("peer", "p1", "First line of its task", send(lead.name, "done")))
	lead.mail("done")
	e.waitStatus("p1", "idle")
	lead.ok(do("agent", map[string]any{"action": "spawn", "template": "advisor", "name": "review", "task": "brief"}))
	e.waitStatus("review-advisor", "idle")
	e.session(dir("solo"))

	golden(t, "ps.view.json", normalize(t, e.squad("ps", "--view", "--no-start"), tmp))
	golden(t, "tail.view.json", normalize(t, e.squad("tail", "p1", "--view", "--no-start"), tmp))

	if _, err := exec.LookPath("node"); err != nil {
		t.Log("no node: the plugin's side is not checked")
		return
	}
	cmd := exec.Command("node", "--test", "test/contract.test.ts")
	cmd.Dir = filepath.Join("..", "extensions", "paseo")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the plugin does not read the golden files as they are: %v\n%s", err, out)
	}
}

var (
	clock    = regexp.MustCompile(`\b\d\d:\d\d:\d\d\b`)
	duration = regexp.MustCompile(`\b\d+(\.\d+)?m?s\b`)
	idLike   = regexp.MustCompile(`^[pt]_[0-9a-f]+$`)
)

// normalize makes what changes from run to run fixed: times, pids, ids (numbered in order of
// appearance), and paths under the test's directory (written /tmp/..., with forward slashes).
func normalize(t *testing.T, doc, tmp string) []byte {
	t.Helper()
	var v any
	must(t, json.Unmarshal([]byte(doc), &v))
	ids := map[string]string{}
	var walk func(key string, v any) any
	walk = func(key string, v any) any {
		switch x := v.(type) {
		case map[string]any:
			for _, k := range slices.Sorted(maps.Keys(x)) { // in order: ids are numbered as met
				x[k] = walk(k, x[k])
			}
			return x
		case []any:
			for i, val := range x {
				x[i] = walk(key, val)
			}
			return x
		case float64:
			if key == "pid" {
				return 1
			}
			return x
		case string:
			switch {
			case x == "":
				return x
			case key == "at" || key == "started_at" || key == "created_at" || key == "last_active":
				return "2026-01-01T00:00:00Z"
			case key == "age":
				return "1m"
			case key == "id" && idLike.MatchString(x):
				if _, ok := ids[x]; !ok {
					ids[x] = x[:2] + string(rune('a'+len(ids)))
				}
				return ids[x]
			}
			if strings.Contains(x, tmp) {
				x = strings.ReplaceAll(strings.ReplaceAll(x, tmp, "/tmp"), `\`, "/")
			}
			return duration.ReplaceAllString(clock.ReplaceAllString(x, "00:00:00"), "1s")
		}
		return v
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	must(t, enc.Encode(walk("", v)))
	return b.Bytes()
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		must(t, os.MkdirAll("testdata", 0o700))
		must(t, os.WriteFile(path, got, 0o600))
		return
	}
	want, err := os.ReadFile(path)
	must(t, err)
	if string(want) != string(got) {
		t.Errorf("%s changed (go test ./e2e -run TestViewContract -update, if on purpose); got:\n%s", path, got)
	}
}
