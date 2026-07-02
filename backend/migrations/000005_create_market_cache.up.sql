CREATE TABLE IF NOT EXISTS market_cache (
    symbol VARCHAR(20) NOT NULL,
    price_usd DECIMAL(20,8) NOT NULL,
    price_idr DECIMAL(20,2) NOT NULL,
    change_pct_24h DECIMAL(5,2) NOT NULL,
    last_fetched TIMESTAMP NOT NULL,
    source VARCHAR(50) NOT NULL,
    PRIMARY KEY (symbol)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
