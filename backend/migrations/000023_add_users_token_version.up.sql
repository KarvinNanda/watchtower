-- Changing the password bumps the version; tokens carrying an older version
-- stop working, which logs out every other session. Existing accounts start
-- at 0, the version of every token issued before this migration.
ALTER TABLE users
    ADD COLUMN token_version INT UNSIGNED NOT NULL DEFAULT 0 AFTER password_hash;
