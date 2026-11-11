-- Per-channel permission overrides replace the stage/announce
-- modes and the per-channel role lists.
--
-- New permission bits (roles.permissions, spaces.everyone_perms):
--   1024 connect · 2048 speak · 4096 stream (share screen)
--   8192 send messages · 16384 attach files · 32768 add reactions
-- Everyone had all of them so far, so @everyone gets them (64512).
UPDATE spaces SET everyone_perms = everyone_perms | 64512;

-- One row per channel and role. role_id is a role or the space ID for
-- @everyone (which has no row in roles), hence no foreign key on it; a
-- trigger removes the rows of a deleted role.
CREATE TABLE channel_overrides (
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  role_id    INTEGER NOT NULL,
  allow      INTEGER NOT NULL DEFAULT 0,
  deny       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (channel_id, role_id)
);
CREATE INDEX channel_overrides_role ON channel_overrides(role_id);
CREATE TRIGGER roles_drop_overrides AFTER DELETE ON roles
BEGIN
  DELETE FROM channel_overrides WHERE role_id = OLD.id;
END;

-- Carry the old rules over:
--   announce → @everyone may not send (8192), the author roles may;
--   stage    → @everyone may not speak or stream (6144), the stage roles may;
--   locked   → @everyone may not connect (1024), the listed roles may.
INSERT INTO channel_overrides(channel_id, role_id, deny)
  SELECT id, space_id, 8192 FROM channels WHERE mode = 'announce' AND space_id IS NOT NULL;
INSERT INTO channel_overrides(channel_id, role_id, deny)
  SELECT id, space_id, 6144 FROM channels WHERE mode = 'stage' AND space_id IS NOT NULL;
INSERT INTO channel_overrides(channel_id, role_id, deny)
  SELECT id, space_id, 1024 FROM channels WHERE locked = 1 AND mode <> 'away' AND space_id IS NOT NULL
  ON CONFLICT(channel_id, role_id) DO UPDATE SET deny = deny | excluded.deny;
INSERT INTO channel_overrides(channel_id, role_id, allow)
  SELECT cr.channel_id, cr.role_id, CASE c.kind WHEN 'text' THEN 8192 ELSE 6144 END
  FROM channel_roles cr JOIN channels c ON c.id = cr.channel_id
  WHERE cr.kind = 'talk' AND c.mode IN ('announce', 'stage');
INSERT INTO channel_overrides(channel_id, role_id, allow)
  SELECT cr.channel_id, cr.role_id, 1024
  FROM channel_roles cr JOIN channels c ON c.id = cr.channel_id
  WHERE cr.kind = 'join' AND c.locked = 1 AND c.mode <> 'away'
  ON CONFLICT(channel_id, role_id) DO UPDATE SET allow = allow | excluded.allow;

UPDATE channels SET mode = 'open' WHERE mode IN ('announce', 'stage');
DROP TABLE channel_roles;
ALTER TABLE channels DROP COLUMN locked;

-- Audio quality of a voice channel: 'speech' | 'high' | 'music'. Clients
-- pick their microphone bitrate from it.
ALTER TABLE channels ADD COLUMN audio TEXT NOT NULL DEFAULT 'speech';
