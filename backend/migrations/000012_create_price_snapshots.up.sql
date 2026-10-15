CREATE TABLE IF NOT EXISTS price_snapshots (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    symbol VARCHAR(20) NOT NULL,
    price DECIMAL(20,2) NOT NULL,
    currency CHAR(3) NOT NULL,
    observed_at TIMESTAMP NOT NULL,
    PRIMARY KEY (id),
    KEY idx_price_snapshots_symbol_observed (symbol, observed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
