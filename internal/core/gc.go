package core

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// gc keeps squad's database from growing for ever: a team closed longer than gc.closed_after, and a
// solo session gone that long, are archived to a file, read back, and only then deleted with their
// mail, logs and run scratch. Archives older than gc.archive_keep are deleted. Nothing is deleted
// that was not first archived and read back the same.

// GCArgs say what to collect.
type GCArgs struct {
	ClosedBefore time.Time     // teams closed and solos gone before this
	DryRun       bool          // list what would go, change nothing
	ArchiveDir   string        // where archives are written
	ArchiveKeep  time.Duration // archives older than this are deleted (0: kept)
}

// GCUnit is one closed team or gone solo.
type GCUnit struct {
	Kind    string         `json:"kind"` // team | solo
	Name    string         `json:"name"`
	Archive string         `json:"archive,omitempty"`
	Counts  map[string]int `json:"counts"`
	Skipped string         `json:"skipped,omitempty"`
}

// GCResult is what a gc did (or would do, dry).
type GCResult struct {
	Units           []GCUnit `json:"units"`
	ExpiredArchives []string `json:"expired_archives,omitempty"`
}

// Archive is one archive file: the rows of one unit, table by table.
type Archive struct {
	Kind   string                      `json:"kind"`
	Name   string                      `json:"name"`
	At     string                      `json:"at"`
	Tables map[string][]map[string]any `json:"tables"`
	Names  map[string]string           `json:"names,omitempty"` // the name of each id the mail names
}

// gcUnit is what one archive holds and one transaction deletes.
type gcUnit struct {
	GCUnit
	id      string              // the team or participant id: archive names stay apart
	members []string            // participant ids whose logs and run dirs go too
	queries map[string]gcSelect // table -> its rows of the unit
}

type gcSelect struct {
	where string
	args  []any
}

// GC archives, verifies and deletes old closed teams and gone solos.
func (e *Engine) GC(a GCArgs) (GCResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	res := GCResult{Units: []GCUnit{}}
	cut := a.ClosedBefore.UTC().Format(time.RFC3339)
	units, err := e.gcUnits(cut)
	if err != nil {
		return res, err
	}
	for _, u := range units {
		if !a.DryRun {
			e.collect(&u, a.ArchiveDir)
		}
		res.Units = append(res.Units, u.GCUnit)
	}
	if !a.DryRun && a.ArchiveKeep > 0 {
		entries, _ := os.ReadDir(a.ArchiveDir)
		for _, en := range entries {
			info, err := en.Info()
			if err != nil || en.IsDir() || !strings.HasSuffix(en.Name(), ".json") || time.Since(info.ModTime()) < a.ArchiveKeep {
				continue
			}
			if os.Remove(filepath.Join(a.ArchiveDir, en.Name())) == nil {
				res.ExpiredArchives = append(res.ExpiredArchives, en.Name())
			}
		}
	}
	return res, nil
}

// gcUnits are the units older than cut: closed teams, and gone solos no open team names as its
// gate or as a taskforce's caller.
func (e *Engine) gcUnits(cut string) ([]gcUnit, error) {
	var out []gcUnit
	teams, err := e.tList(`status=? AND closed_at!='' AND closed_at<?`, TeamClosed, cut)
	if err != nil {
		return nil, err
	}
	for _, t := range teams {
		members, _ := e.pList(`team_id=?`, t.ID)
		ids := make([]string, 0, len(members))
		for _, m := range members {
			ids = append(ids, m.ID)
		}
		in, args := inList(ids)
		u := gcUnit{GCUnit: GCUnit{Kind: "team", Name: t.Name}, id: t.ID, members: ids, queries: map[string]gcSelect{
			"teams":        {`id=?`, []any{t.ID}},
			"participants": {`team_id=?`, []any{t.ID}},
			"mail":         {`team_id=? OR to_id IN ` + in, append([]any{t.ID}, args...)},
			"fired":        {`key LIKE ?`, []any{t.ID + "/%"}},
		}}
		e.countUnit(&u)
		out = append(out, u)
	}
	solos, err := e.pList(`kind=? AND team_id='' AND status=? AND last_active<?
		AND id NOT IN (SELECT gate_id FROM teams WHERE status='open') AND id NOT IN (SELECT parent_id FROM teams WHERE status='open')`,
		KindSession, StatusGone, cut)
	if err != nil {
		return nil, err
	}
	for _, s := range solos {
		u := gcUnit{GCUnit: GCUnit{Kind: "solo", Name: s.Name}, id: s.ID, members: []string{s.ID}, queries: map[string]gcSelect{
			"participants": {`id=?`, []any{s.ID}},
			"mail":         {`to_id=? AND team_id NOT IN (SELECT id FROM teams)`, []any{s.ID}}, // a team's mail goes with its team
		}}
		e.countUnit(&u)
		out = append(out, u)
	}
	return out, nil
}

func (e *Engine) countUnit(u *gcUnit) {
	u.Counts = map[string]int{}
	for table, q := range u.queries {
		var n int
		_ = e.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+q.where, q.args...).Scan(&n)
		u.Counts[table] = n
	}
}

// collect archives u, reads the archive back, and deletes u's rows when it holds them all.
func (e *Engine) collect(u *gcUnit, dir string) {
	arch := Archive{Kind: u.Kind, Name: u.Name, At: now(), Tables: map[string][]map[string]any{}}
	for table, q := range u.queries {
		rows, err := e.rows(table, q)
		if err != nil {
			u.Skipped = "read: " + err.Error()
			return
		}
		arch.Tables[table] = rows
	}
	// Mail names others by id (a gate gone solo, another team's gate): their names go along.
	arch.Names = map[string]string{}
	for _, m := range arch.Tables["mail"] {
		for _, k := range []string{"from_id", "to_id"} {
			id, _ := m[k].(string)
			if _, ok := arch.Names[id]; ok || id == "" {
				continue
			}
			var name string
			_ = e.db.QueryRow(`SELECT name FROM participants WHERE id=?`, id).Scan(&name)
			arch.Names[id] = cmp.Or(name, id)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		u.Skipped = err.Error()
		return
	}
	// Names repeat (three teams called fixers): the id keeps each archive apart, and an existing
	// file is never written over.
	name := fmt.Sprintf("%s-%s-%s-%s.json", time.Now().UTC().Format("20060102-150405"), u.Kind, cleanName(u.Name), cleanName(u.id))
	path := filepath.Join(dir, name)
	b, _ := json.MarshalIndent(arch, "", " ")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		u.Skipped = err.Error()
		return
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	// From here a unit that is kept takes its archive back.
	keep := func(why string) {
		u.Skipped = why
		_ = os.Remove(path)
	}
	if err != nil {
		keep(err.Error())
		return
	}
	back, err := ReadArchive(path)
	if err != nil {
		keep("archive read back: " + err.Error())
		return
	}
	for table, n := range u.Counts {
		if len(back.Tables[table]) != n {
			keep("archive read back does not match the database")
			return
		}
	}
	tx, err := e.db.Begin()
	if err != nil {
		keep(err.Error())
		return
	}
	for _, table := range []string{"fired", "mail", "participants", "teams"} {
		q, ok := u.queries[table]
		if !ok {
			continue
		}
		r, err := tx.Exec(`DELETE FROM `+table+` WHERE `+q.where, q.args...)
		if err != nil {
			_ = tx.Rollback()
			keep(err.Error())
			return
		}
		if n, _ := r.RowsAffected(); int(n) != u.Counts[table] {
			_ = tx.Rollback()
			keep("the rows changed since the archive was written")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		keep(err.Error())
		return
	}
	for _, id := range u.members {
		_ = os.Remove(WorkerLog(e.dir, id))
		_ = os.RemoveAll(RunDir(e.dir, id))
	}
	u.Archive = name
	e.event("gc", u.Name, "%s %s archived to %s and deleted (%d mail)", u.Kind, u.Name, name, u.Counts["mail"])
}

// rows reads every column of table's rows of q as maps.
func (e *Engine) rows(table string, q gcSelect) ([]map[string]any, error) {
	rs, err := e.db.Query(`SELECT * FROM `+table+` WHERE `+q.where, q.args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	cols, _ := rs.Columns()
	out := []map[string]any{}
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := map[string]any{}
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				row[c] = string(b)
			} else {
				row[c] = vals[i]
			}
		}
		out = append(out, row)
	}
	return out, rs.Err()
}

// ReadArchive reads an archive file.
func ReadArchive(path string) (Archive, error) {
	var a Archive
	b, err := os.ReadFile(path)
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(b, &a)
	return a, err
}

// inList is "(?,?,…)" and its args, "(”)" for none.
func inList(ids []string) (string, []any) {
	if len(ids) == 0 {
		return "('')", nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")", args
}
