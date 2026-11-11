-- Guest access: an invite can admit people without an account. A guest is
-- a user row without a password that only lives as long as its session.
ALTER TABLE users ADD COLUMN is_guest INTEGER NOT NULL DEFAULT 0;
ALTER TABLE invites ADD COLUMN guest INTEGER NOT NULL DEFAULT 0;
