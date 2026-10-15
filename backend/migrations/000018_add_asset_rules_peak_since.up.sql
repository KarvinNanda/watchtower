-- #21: when a trailing stop started tracking its peak (creation, re-arm or,
-- for rules that existed before this migration, their next evaluation).
ALTER TABLE asset_rules
    ADD COLUMN peak_since TIMESTAMP NULL AFTER peak_price_usd;
