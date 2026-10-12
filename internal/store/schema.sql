-- squad database schema, version 1: a new database gets it (user_version 0), then the migrations.
-- IF NOT EXISTS keeps it safe on a database made before versions were kept (0.1.x).
CREATE TABLE IF NOT EXISTS teams (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL,
	template   TEXT NOT NULL,
	root       TEXT NOT NULL,
	manifest   TEXT NOT NULL,
	gate_id    TEXT NOT NULL DEFAULT '',
	parent_id  TEXT NOT NULL DEFAULT '',
	status     TEXT NOT NULL DEFAULT 'open',
	created_at TEXT NOT NULL,
	closed_at  TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS participants (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL UNIQUE,
	team_id     TEXT NOT NULL DEFAULT '',
	role        TEXT NOT NULL DEFAULT '',
	kind        TEXT NOT NULL,
	status      TEXT NOT NULL,
	cwd         TEXT NOT NULL,
	session_ref TEXT NOT NULL DEFAULT '',
	spawned_by  TEXT NOT NULL DEFAULT '',
	model       TEXT NOT NULL DEFAULT '',
	thinking    TEXT NOT NULL DEFAULT '',
	token       TEXT NOT NULL UNIQUE,
	task        TEXT NOT NULL DEFAULT '',
	pid         INTEGER NOT NULL DEFAULT 0,
	depth       INTEGER NOT NULL DEFAULT 0,
	respawns    TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL,
	last_active TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS mail (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	team_id      TEXT NOT NULL DEFAULT '',
	from_id      TEXT NOT NULL,
	to_id        TEXT NOT NULL,
	body         TEXT NOT NULL,
	created_at   TEXT NOT NULL,
	batch        INTEGER NOT NULL DEFAULT 0,
	delivered_at TEXT NOT NULL DEFAULT '',
	acked_at     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS mail_to ON mail(to_id, acked_at);
CREATE TABLE IF NOT EXISTS events (
	id   INTEGER PRIMARY KEY AUTOINCREMENT,
	at   TEXT NOT NULL,
	kind TEXT NOT NULL,
	team TEXT NOT NULL DEFAULT '',
	text TEXT NOT NULL
);
