ALTER TABLE market_cache
    ADD COLUMN change_pct_7d DECIMAL(10,4) NULL AFTER change_pct_24h;
