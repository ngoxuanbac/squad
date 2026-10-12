package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite"
)

func columns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[n] = true
	}
	return out
}

// Version is the schema version Open brings a database to.
func Version() int {
	ms, _ := migrationsIn(files)
	if len(ms) == 0 {
		return 1
	}
	return ms[len(ms)-1].version
}

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestOpenFreshIsLatest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "squad.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := userVersion(t, db); got != Version() || got < 2 {
		t.Fatalf("user_version %d, want %d", got, Version())
	}
	for _, table := range []string{"teams", "participants", "mail", "events"} {
		if len(columns(t, db, table)) == 0 {
			t.Errorf("no table %s", table)
		}
	}
	if !columns(t, db, "participants")["harness"] {
		t.Error("participants has no harness column")
	}
	// Opening again changes nothing and does not fail.
	db.Close()
	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if got := userVersion(t, db2); got != Version() {
		t.Fatalf("reopen: user_version %d", got)
	}
}

// A database made before versions were kept (0.1.x: the schema, user_version 0) gets the
// migrations, and its rows keep their values with the new columns' defaults.
func TestOpenMigratesOldDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "squad.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	schema, _ := files.ReadFile("schema.sql")
	if _, err := old.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO participants(id,name,kind,status,cwd,token,created_at) VALUES('p1','w','worker','stopped','.','tok','now')`); err != nil {
		t.Fatal(err)
	}
	old.Close()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var harness string
	if err := db.QueryRow(`SELECT harness FROM participants WHERE id='p1'`).Scan(&harness); err != nil {
		t.Fatal(err)
	}
	if harness != "claude" {
		t.Fatalf("old row harness %q, want claude", harness)
	}
	if got := userVersion(t, db); got != Version() {
		t.Fatalf("user_version %d, want %d", got, Version())
	}
}

func TestOpenBadPath(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "missing", "dir", "squad.db")); err == nil {
		t.Fatal("open in a missing directory did not fail")
	}
}

// A failing migration rolls back and leaves the database at the version before it.
func TestApplyStopsAtAFailingMigration(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ms := []migration{
		{2, "CREATE TABLE a (x INTEGER);"},
		{3, "CREATE TABLE b (y INTEGER); THIS IS NOT SQL;"},
		{4, "CREATE TABLE c (z INTEGER);"},
	}
	if err := apply(db, "CREATE TABLE base (id INTEGER);", ms); err == nil || !strings.Contains(err.Error(), "migration 3") {
		t.Fatalf("apply: %v", err)
	}
	if v := userVersion(t, db); v != 2 {
		t.Fatalf("user_version %d, want 2", v)
	}
	if len(columns(t, db, "b")) != 0 || len(columns(t, db, "c")) != 0 {
		t.Fatal("a failed or later migration left a table")
	}
	if err := apply(db, "", nil); err != nil {
		t.Fatalf("nothing to do: %v", err)
	}
	if err := apply(db, "NOT SQL", nil); err != nil {
		t.Fatalf("a versioned database must not rerun the schema: %v", err)
	}
	fresh, _ := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "y.db"))
	defer fresh.Close()
	if err := apply(fresh, "NOT SQL", nil); err == nil {
		t.Fatal("bad schema accepted")
	}
}

func TestMigrationsIn(t *testing.T) {
	ms, err := migrationsIn(fstest.MapFS{
		"migrate_10.sql": {Data: []byte("ten")},
		"migrate_2.sql":  {Data: []byte("two")},
		"schema.sql":     {Data: []byte("base")},
	})
	if err != nil || len(ms) != 2 || ms[0].version != 2 || ms[1].version != 10 || ms[1].sql != "ten" {
		t.Fatalf("%+v %v", ms, err)
	}
	if _, err := migrationsIn(fstest.MapFS{"migrate_x.sql": {}}); err == nil {
		t.Fatal("bad name accepted")
	}
}
