-- Lightvoice initial schema. IDs are snowflakes (INTEGER), times are unix ms.

CREATE TABLE users (
  id              INTEGER PRIMARY KEY,
  username        TEXT NOT NULL UNIQUE COLLATE NOCASE,
  display_name    TEXT NOT NULL,
  password_hash   TEXT NOT NULL,
  avatar_key      TEXT,
  status          TEXT NOT NULL DEFAULT 'online' CHECK (status IN ('online','idle','dnd','invisible')),
  status_text     TEXT NOT NULL DEFAULT '',
  created_at      INTEGER NOT NULL,
  disabled_at     INTEGER,
  is_server_admin INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE devices (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  public_key   BLOB,
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  revoked_at   INTEGER
);
CREATE INDEX devices_user ON devices(user_id);

CREATE TABLE sessions (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id    INTEGER REFERENCES devices(id) ON DELETE SET NULL,
  token_hash   BLOB NOT NULL UNIQUE,
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL,
  user_agent   TEXT NOT NULL DEFAULT '',
  revoked_at   INTEGER
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE spaces (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  icon_key   TEXT,
  owner_id   INTEGER NOT NULL REFERENCES users(id),
  created_at INTEGER NOT NULL,
  e2ee_epoch INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE space_members (
  space_id  INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role      TEXT NOT NULL CHECK (role IN ('owner','admin','member')),
  joined_at INTEGER NOT NULL,
  PRIMARY KEY (space_id, user_id)
);
CREATE INDEX space_members_user ON space_members(user_id);

CREATE TABLE bans (
  space_id   INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  banned_by  INTEGER NOT NULL,
  reason     TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  PRIMARY KEY (space_id, user_id)
);

CREATE TABLE invites (
  code        TEXT PRIMARY KEY,
  space_id    INTEGER REFERENCES spaces(id) ON DELETE CASCADE,
  created_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
  max_uses    INTEGER,
  uses        INTEGER NOT NULL DEFAULT 0,
  expires_at  INTEGER,
  revoked_at  INTEGER,
  grants_admin INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL
);

CREATE TABLE channels (
  id              INTEGER PRIMARY KEY,
  space_id        INTEGER REFERENCES spaces(id) ON DELETE CASCADE,
  kind            TEXT NOT NULL CHECK (kind IN ('text','voice','dm','group_dm','category')),
  parent_id       INTEGER,
  name            TEXT NOT NULL DEFAULT '',
  topic           TEXT NOT NULL DEFAULT '',
  position        INTEGER NOT NULL DEFAULT 0,
  user_limit      INTEGER,
  e2ee_epoch      INTEGER NOT NULL DEFAULT 1,
  owner_id        INTEGER,
  created_at      INTEGER NOT NULL,
  last_message_id INTEGER
);
CREATE INDEX channels_space ON channels(space_id);

CREATE TABLE channel_members (
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (channel_id, user_id)
);
CREATE INDEX channel_members_user ON channel_members(user_id);

CREATE TABLE messages (
  id                INTEGER PRIMARY KEY,
  channel_id        INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  author_id         INTEGER NOT NULL REFERENCES users(id),
  content           TEXT NOT NULL DEFAULT '',
  reply_to_id       INTEGER,
  mentions_everyone INTEGER NOT NULL DEFAULT 0,
  edited_at         INTEGER,
  deleted_at        INTEGER,
  created_at        INTEGER NOT NULL
);
CREATE INDEX messages_channel ON messages(channel_id, id DESC);
CREATE INDEX messages_author ON messages(author_id);

CREATE TABLE message_mentions (
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (message_id, user_id)
);

CREATE VIRTUAL TABLE messages_fts USING fts5(
  content, content='messages', content_rowid='id', tokenize='unicode61 remove_diacritics 2'
);
CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
  INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
END;
CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.id, old.content);
END;
CREATE TRIGGER messages_au AFTER UPDATE OF content ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.id, old.content);
  INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
END;

CREATE TABLE reactions (
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  emoji      TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (message_id, user_id, emoji)
);

CREATE TABLE pins (
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  pinned_by  INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (channel_id, message_id)
);

CREATE TABLE attachments (
  id          INTEGER PRIMARY KEY,
  message_id  INTEGER REFERENCES messages(id) ON DELETE SET NULL,
  uploader_id INTEGER NOT NULL REFERENCES users(id),
  channel_id  INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  filename    TEXT NOT NULL,
  mime        TEXT NOT NULL,
  size_bytes  INTEGER NOT NULL,
  sha256      BLOB NOT NULL,
  width       INTEGER,
  height      INTEGER,
  storage_key TEXT NOT NULL,
  thumb_key   TEXT,
  attached    INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL
);
CREATE INDEX attachments_message ON attachments(message_id);
CREATE INDEX attachments_orphans ON attachments(attached, created_at);

CREATE TABLE read_states (
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  channel_id    INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  last_read_id  INTEGER NOT NULL DEFAULT 0,
  mention_count INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, channel_id)
);

CREATE TABLE notification_settings (
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  target_id   INTEGER NOT NULL,
  level       TEXT NOT NULL CHECK (level IN ('all','mentions','none')),
  muted_until INTEGER,
  PRIMARY KEY (user_id, target_id)
);

CREATE TABLE relationships (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  other_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL CHECK (kind IN ('friend','pending_out','pending_in','blocked')),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, other_id)
);

CREATE TABLE media_key_envelopes (
  container_id      INTEGER NOT NULL,
  epoch             INTEGER NOT NULL,
  device_id         INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  ciphertext        BLOB NOT NULL,
  created_by_device INTEGER NOT NULL,
  created_at        INTEGER NOT NULL,
  PRIMARY KEY (container_id, epoch, device_id)
);

CREATE TABLE user_settings (
  user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  json       TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE audit_log (
  id         INTEGER PRIMARY KEY,
  actor_id   INTEGER,
  space_id   INTEGER,
  action     TEXT NOT NULL,
  target_id  INTEGER,
  meta_json  TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL
);

CREATE TABLE webhook_events (
  id          TEXT PRIMARY KEY,
  received_at INTEGER NOT NULL
);

CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
