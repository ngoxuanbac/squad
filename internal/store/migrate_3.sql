-- 3: a session's turns, as its harness's hooks report them (squad setup claude installs them).
-- hooked: the session has the hooks, so its mail is given with its turns and acked when a turn
-- ends normally, and an idle one is woken. turn: the open turn's key (Claude's prompt_id), ''
-- when none; turn_seq numbers its turns (mail.batch of what a turn was given); turn_blocks counts
-- how often the turn's end was held back to read new mail.
ALTER TABLE participants ADD COLUMN hooked INTEGER NOT NULL DEFAULT 0;
ALTER TABLE participants ADD COLUMN turn TEXT NOT NULL DEFAULT '';
ALTER TABLE participants ADD COLUMN turn_seq INTEGER NOT NULL DEFAULT 0;
ALTER TABLE participants ADD COLUMN turn_blocks INTEGER NOT NULL DEFAULT 0;
