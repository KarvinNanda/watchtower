-- #21: the source high and the start of the window it covers. Additive only.
ALTER TABLE market_cache
    ADD COLUMN high_usd DECIMAL(20,8) NULL AFTER week52_low_usd,
    ADD COLUMN high_since TIMESTAMP NULL AFTER high_usd;
