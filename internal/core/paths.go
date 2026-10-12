package core

import "path/filepath"

// Paths under squad's home directory (the daemon passes it in Options.Dir).

// DBPath is the database.
func DBPath(dir string) string { return filepath.Join(dir, "squad.db") }

// TemplatesDir holds the user's own templates, one directory each.
func TemplatesDir(dir string) string { return filepath.Join(dir, "templates") }

// LogsDir holds one stream-json log per worker.
func LogsDir(dir string) string { return filepath.Join(dir, "logs") }

// WorkerLog is the log of participant id's worker runs.
func WorkerLog(dir, id string) string { return filepath.Join(LogsDir(dir), id+".jsonl") }

// RunDir is the scratch directory of participant id's runs (its MCP config).
func RunDir(dir, id string) string { return filepath.Join(dir, "run", id) }
