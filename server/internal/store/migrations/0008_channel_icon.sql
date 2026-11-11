-- Optional icon per text/voice channel ('' = default # / speaker).
ALTER TABLE channels ADD COLUMN icon TEXT NOT NULL DEFAULT '';
