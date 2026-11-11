-- Roles with permissions instead of fixed admin/member tiers.
-- The owner stays spaces.owner_id (space_members.role is 'owner' or 'member');
-- @everyone is implicit and keeps its permissions on the space itself.

ALTER TABLE spaces ADD COLUMN everyone_perms INTEGER NOT NULL DEFAULT 0;

CREATE TABLE roles (
  id          INTEGER PRIMARY KEY,
  space_id    INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  position    INTEGER NOT NULL,          -- 1 = lowest; @everyone is 0
  permissions INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL
);
CREATE INDEX roles_space ON roles(space_id, position);

CREATE TABLE member_roles (
  space_id INTEGER NOT NULL,
  user_id  INTEGER NOT NULL,
  role_id  INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  PRIMARY KEY (space_id, user_id, role_id),
  FOREIGN KEY (space_id, user_id) REFERENCES space_members(space_id, user_id) ON DELETE CASCADE
);
CREATE INDEX member_roles_role ON member_roles(role_id);

-- Every existing space gets an "Admin" role with the Administrator permission
-- (bit 9 = 512), and today's admins move into it. The role ID is the space's
-- snowflake plus 2^21 in the sequence bits: unique per space (spaces created
-- in the same millisecond differ in their sequence), it never reaches the
-- next millisecond, and all later IDs are newer.
INSERT INTO roles(id, space_id, name, position, permissions, created_at)
  SELECT id + 2097152, id, 'Admin', 1, 512, created_at FROM spaces;
INSERT INTO member_roles(space_id, user_id, role_id)
  SELECT space_id, user_id, space_id + 2097152 FROM space_members WHERE role = 'admin';
UPDATE space_members SET role = 'member' WHERE role = 'admin';
