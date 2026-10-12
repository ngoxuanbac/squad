-- 2: each participant names its harness (claude; codex later). Existing rows were all Claude.
ALTER TABLE participants ADD COLUMN harness TEXT NOT NULL DEFAULT 'claude';
