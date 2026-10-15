ALTER TABLE market_cache
    MODIFY change_pct_24h DECIMAL(5,2) NULL,
    ADD COLUMN week52_high_usd DECIMAL(20,8) NULL AFTER change_pct_24h,
    ADD COLUMN week52_low_usd DECIMAL(20,8) NULL AFTER week52_high_usd;
