-- 4: a gone member woken by its mail runs as a headless worker in its own session (was_session),
-- once per mail (woke_mail: the newest mail that woke it); opening the session again takes it back.
ALTER TABLE participants ADD COLUMN was_session INTEGER NOT NULL DEFAULT 0;
ALTER TABLE participants ADD COLUMN woke_mail INTEGER NOT NULL DEFAULT 0;
