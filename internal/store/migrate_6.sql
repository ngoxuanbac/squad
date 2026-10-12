-- 6: columns nothing reads: participants.pid (a worker's process id; the daemon's runs know it) and
-- mail.client_msg_id (an idempotency key no tool could send) with its index.
DROP INDEX IF EXISTS mail_client_msg;
ALTER TABLE mail DROP COLUMN client_msg_id;
ALTER TABLE participants DROP COLUMN pid;
