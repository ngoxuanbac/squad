// Package store opens the squad SQLite database and applies the schema: schema.sql is version 1,
// and migrate_N.sql brings a database from version N-1 to N (PRAGMA user_version).
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // pure Go: builds for Windows and Linux without cgo
)

//go:embed schema.sql migrate_*.sql
var files embed.FS

// Open opens the database at path and brings it to the latest version. One connection: the
// engine serializes every change under its own lock anyway.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	schema, _ := files.ReadFile("schema.sql") // embedded: there
	ms, err := migrationsIn(files)
	if err == nil {
		err = apply(db, string(schema), ms)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

type migration struct {
	version int
	sql     string
}

// migrationsIn reads the migrate_N.sql files of fsys, in order.
func migrationsIn(fsys fs.ReadDirFS) ([]migration, error) {
	names, err := fsys.ReadDir(".")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, n := range names {
		v, ok := strings.CutPrefix(n.Name(), "migrate_")
		if !ok {
			continue
		}
		num, err := strconv.Atoi(strings.TrimSuffix(v, ".sql"))
		if err != nil {
			return nil, fmt.Errorf("migration %s: bad number", n.Name())
		}
		b, err := fs.ReadFile(fsys, n.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{num, string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// apply brings db to the last of ms: schema when it has no version yet, then each newer migration
// in a transaction of its own, so a failing one leaves the database at the version before it.
func apply(db *sql.DB, schema string, ms []migration) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v == 0 {
		if _, err := db.Exec(schema); err != nil {
			return fmt.Errorf("schema: %w", err)
		}
		v = 1
		if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
			return err
		}
	}
	for _, m := range ms {
		if m.version <= v {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, m.version)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		v = m.version
	}
	return nil
}
