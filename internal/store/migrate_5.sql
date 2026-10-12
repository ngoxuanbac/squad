-- 5: the full mail model, and what the watch rules and notices read.
-- mail: kind (a free label), reply_to and target (#N, i.e. mail ids), op (assign | replace |
-- remove), client_msg_id (the sender's idempotency key), cc_of (the original of a routing cc
-- copy), held_reason (policy_hold: over the rate limit, until an admin releases it), gives (how
-- often it was handed over: more than once is a redelivery).
ALTER TABLE mail ADD COLUMN kind TEXT NOT NULL DEFAULT '';
ALTER TABLE mail ADD COLUMN reply_to INTEGER NOT NULL DEFAULT 0;
ALTER TABLE mail ADD COLUMN op TEXT NOT NULL DEFAULT '';
ALTER TABLE mail ADD COLUMN target INTEGER NOT NULL DEFAULT 0;
ALTER TABLE mail ADD COLUMN client_msg_id TEXT NOT NULL DEFAULT '';
ALTER TABLE mail ADD COLUMN cc_of INTEGER NOT NULL DEFAULT 0;
ALTER TABLE mail ADD COLUMN held_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE mail ADD COLUMN gives INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX IF NOT EXISTS mail_client_msg ON mail(from_id, client_msg_id) WHERE client_msg_id != '';
CREATE INDEX IF NOT EXISTS mail_from ON mail(from_id, created_at);

-- participants: since when the status is what it is (kept by the trigger), and the end of the
-- last turn (the watch rules' silence clock).
ALTER TABLE participants ADD COLUMN state_since TEXT NOT NULL DEFAULT '';
ALTER TABLE participants ADD COLUMN last_turn_end TEXT NOT NULL DEFAULT '';
CREATE TRIGGER IF NOT EXISTS participants_state_since AFTER UPDATE OF status ON participants
WHEN NEW.status != OLD.status
BEGIN
	UPDATE participants SET state_since = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE id = NEW.id;
END;

-- teams: the idle stretch of a taskforce its caller was told about (one notice per stretch).
ALTER TABLE teams ADD COLUMN idle_noticed TEXT NOT NULL DEFAULT '';

-- participants.turn_mail: the newest mail id when the open turn started (what the gate sent after
-- it decides its notice; ids, since times are only to the second).
ALTER TABLE participants ADD COLUMN turn_mail INTEGER NOT NULL DEFAULT 0;

-- fired: the watch-rule incidents already told (one notice per incident).
CREATE TABLE IF NOT EXISTS fired (
	key TEXT PRIMARY KEY,
	at  TEXT NOT NULL
);
