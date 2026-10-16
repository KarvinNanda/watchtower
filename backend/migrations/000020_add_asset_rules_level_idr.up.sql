-- #40: the level of an IDR price_level rule. Wider than level_usd: BTC is
-- about Rp 1.5 billion.
ALTER TABLE asset_rules
    ADD COLUMN level_idr DECIMAL(24,8) NULL AFTER level_currency;
