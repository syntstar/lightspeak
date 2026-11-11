-- Private channels. New permission bit 65536 "view channel";
-- without it a channel does not exist for the user. @everyone keeps
-- seeing every channel until an admin restricts one.
UPDATE spaces SET everyone_perms = everyone_perms | 65536;
