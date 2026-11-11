-- Accounts can delete themselves. The row stays (messages keep
-- their author) but is anonymised and can no longer sign in.
ALTER TABLE users ADD COLUMN deleted_at INTEGER;
