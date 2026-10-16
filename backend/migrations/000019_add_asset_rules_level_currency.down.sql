-- Manual rollback only: IDR rules lose their currency (see 000020 down).
ALTER TABLE asset_rules DROP COLUMN level_currency;
