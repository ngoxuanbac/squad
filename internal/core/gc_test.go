package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGC(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	root := t.TempDir()
	lead := must(e.RegisterSession(root, "", "", "claude"))
	must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "lead-peer", Name: "old"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "spawn", Role: "peer", Name: "w1", Task: "t"}))
	must(e.Send(lead.Token, SendArgs{To: "w1", Body: "more"}))
	// Mail to the lead in its team goes with the team, not with the solo the lead becomes.
	must(e.Send(tokenOf(t, e, "w1"), SendArgs{To: lead.Name, Body: "done"}))
	must(e.Agent(lead.Token, AgentArgs{Action: "close"}))
	w1 := mustID(e, "w1")
	eventually(t, e, "w1 stopped", func() bool { return e.runs[w1] == nil })
	_ = os.WriteFile(WorkerLog(e.dir, w1), []byte("log"), 0o600)
	solo := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	keep := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	team := teamOf(t, e, "old")
	e.mu.Lock()
	_, _ = e.db.Exec(`UPDATE teams SET closed_at=? WHERE name='old'`, old)
	_, _ = e.db.Exec(`UPDATE participants SET status=?, last_active=? WHERE id IN (?,?)`, StatusGone, old, solo.ID, lead.ID)
	_, _ = e.db.Exec(`UPDATE participants SET status=? WHERE id=?`, StatusGone, keep.ID) // gone, but lately
	_, _ = e.db.Exec(`INSERT INTO fired(key, at) VALUES(?,?)`, team.ID+"/timers[0]/x/y", old)
	e.mu.Unlock()

	dir := filepath.Join(t.TempDir(), "archive")
	stale := filepath.Join(dir, "stale.json")
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(stale, []byte("{}"), 0o600)
	_ = os.Chtimes(stale, time.Now().Add(-90*24*time.Hour), time.Now().Add(-90*24*time.Hour))
	cut := time.Now().Add(-24 * time.Hour)

	dry := must(e.GC(GCArgs{ClosedBefore: cut, DryRun: true, ArchiveDir: dir, ArchiveKeep: time.Hour}))
	// The lead went solo when its team closed: it is a unit of its own.
	names := map[string]bool{}
	for _, u := range dry.Units[1:] {
		names[u.Name] = true
	}
	if len(dry.Units) != 3 || dry.Units[0].Kind != "team" || dry.Units[0].Counts["participants"] != 1 ||
		dry.Units[0].Counts["fired"] != 1 || !names[solo.Name] || !names[lead.Name] || dry.ExpiredArchives != nil {
		t.Fatalf("dry: %+v", dry)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("dry run deleted an archive")
	}

	res := must(e.GC(GCArgs{ClosedBefore: cut, ArchiveDir: dir, ArchiveKeep: time.Hour}))
	if len(res.Units) != 3 || len(res.ExpiredArchives) != 1 {
		t.Fatalf("gc: %+v", res)
	}
	for _, u := range res.Units {
		if u.Archive == "" || u.Skipped != "" {
			t.Fatalf("unit not archived: %+v", u)
		}
		a := must(ReadArchive(filepath.Join(dir, u.Archive)))
		if a.Name != u.Name || len(a.Tables["participants"]) != u.Counts["participants"] || len(a.Tables["mail"]) != u.Counts["mail"] {
			t.Fatalf("archive %s: %+v", u.Archive, a)
		}
		// The lead went solo: it is no member of the team's archive, but its mail names it.
		if u.Kind == "team" && (a.Names[lead.ID] != lead.Name || a.Names[w1] != "w1") {
			t.Fatalf("archive names: %v", a.Names)
		}
	}
	e.mu.Lock()
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM participants WHERE id IN (?,?,?)`, lead.ID, w1, solo.ID).Scan(&n)
	var teams, fired, mail int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM teams`).Scan(&teams)
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM fired`).Scan(&fired)
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE to_id=?`, w1).Scan(&mail)
	kept, _ := e.byID(keep.ID)
	e.mu.Unlock()
	if n != 0 || teams != 0 || fired != 0 || mail != 0 || kept == nil {
		t.Fatalf("left: participants %d teams %d fired %d mail %d kept %v", n, teams, fired, mail, kept)
	}
	if _, err := os.Stat(WorkerLog(e.dir, w1)); err == nil {
		t.Fatal("worker log kept")
	}
	if again := must(e.GC(GCArgs{ClosedBefore: cut, ArchiveDir: dir})); len(again.Units) != 0 {
		t.Fatalf("second gc: %+v", again)
	}
}

// A gone session that is the gate of an open team is not collected.
func TestGCKeepsOpenGate(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	gate := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	must(e.Agent(gate.Token, AgentArgs{Action: "found", Template: "p2p", Name: "crew"}))
	e.mu.Lock()
	// Its team named it, though gone (as after a leave): it stays.
	_, _ = e.db.Exec(`UPDATE participants SET status=?, team_id='', last_active='2000-01-01T00:00:00Z' WHERE id=?`, StatusGone, gate.ID)
	e.mu.Unlock()
	res := must(e.GC(GCArgs{ClosedBefore: time.Now(), ArchiveDir: t.TempDir()}))
	if len(res.Units) != 0 {
		t.Fatalf("%+v", res)
	}
}

func TestGCArchiveDirFails(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	s := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
	e.mu.Lock()
	_, _ = e.db.Exec(`UPDATE participants SET status=?, last_active='2000-01-01T00:00:00Z' WHERE id=?`, StatusGone, s.ID)
	e.mu.Unlock()
	file := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(file, nil, 0o600)
	res := must(e.GC(GCArgs{ClosedBefore: time.Now(), ArchiveDir: filepath.Join(file, "sub")}))
	if len(res.Units) != 1 || res.Units[0].Skipped == "" || res.Units[0].Archive != "" {
		t.Fatalf("%+v", res)
	}
	if p := pOf(t, e, s.Name); p == nil {
		t.Fatal("deleted without an archive")
	}
	if _, err := ReadArchive(file); err == nil {
		t.Fatal("empty file read as an archive")
	}
	if _, err := ReadArchive(filepath.Join(t.TempDir(), "none")); err == nil {
		t.Fatal("missing archive read")
	}
}

// Two teams of one name collected in the same second keep two archives; a unit whose archive does
// not read back the same is kept, and its archive removed.
func TestGCSameNamesAndKept(t *testing.T) {
	e := memEngine(t, &memRuntime{name: "claude"})
	for range 2 {
		lead := must(e.RegisterSession(t.TempDir(), "", "", "claude"))
		must(e.Agent(lead.Token, AgentArgs{Action: "found", Template: "p2p", Name: "fixers"}))
		must(e.Agent(lead.Token, AgentArgs{Action: "close"}))
	}
	cut := time.Now().Add(time.Hour)
	dir := t.TempDir()

	e.mu.Lock()
	units, err := e.gcUnits(cut.UTC().Format(time.RFC3339))
	if err != nil || len(units) < 2 {
		e.mu.Unlock()
		t.Fatalf("units: %v %v", units, err)
	}
	u := units[0]
	u.Counts["teams"]++ // the database "changed": the read back does not match
	e.collect(&u, dir)
	e.mu.Unlock()
	if u.Skipped == "" || u.Archive != "" {
		t.Fatalf("kept unit: %+v", u.GCUnit)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("archive of a kept unit left: %v", left)
	}

	res := must(e.GC(GCArgs{ClosedBefore: cut, ArchiveDir: dir}))
	files := map[string]bool{}
	for _, u := range res.Units {
		if u.Kind == "team" {
			files[u.Archive] = true
		}
	}
	if len(files) != 2 {
		t.Fatalf("two teams named fixers, archives %v", files)
	}
}
