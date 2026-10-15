ALTER TABLE market_cache
    DROP COLUMN week52_low_usd,
    DROP COLUMN week52_high_usd,
    DROP COLUMN change_pct_24h,
    ADD COLUMN change_pct_24h DECIMAL(5,2) NOT NULL DEFAULT 0 AFTER price_idr;
