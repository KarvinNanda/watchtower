-- Manual rollback only: tokens stop carrying a checked version.
ALTER TABLE users DROP COLUMN token_version;
