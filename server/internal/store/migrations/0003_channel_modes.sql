-- Channel kinds of use and per-channel role lists.
-- mode: text 'open' | 'announce'; voice 'open' | 'stage' | 'away'.
--   announce: everyone reads, only the listed 'talk' roles write.
--   stage:    everyone listens, only the listed 'talk' roles speak.
--   away:     nobody speaks; at most one per space (the server keeps that).
-- slowmode:   seconds between two messages of one person (text).
-- locked:     voice only, joining needs one of the listed 'join' roles.
-- away_after: minutes without activity after which clients move themselves
--             into this away channel (0 = never).
-- Whoever may manage channels is never restricted by any of this.

ALTER TABLE channels ADD COLUMN mode TEXT NOT NULL DEFAULT 'open';
ALTER TABLE channels ADD COLUMN slowmode INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channels ADD COLUMN locked INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channels ADD COLUMN away_after INTEGER NOT NULL DEFAULT 0;

CREATE TABLE channel_roles (
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL CHECK (kind IN ('talk','join')),
  role_id    INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  PRIMARY KEY (channel_id, kind, role_id)
);
CREATE INDEX channel_roles_role ON channel_roles(role_id);
