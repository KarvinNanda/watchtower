CREATE TABLE IF NOT EXISTS asset_subscriptions (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    user_id BIGINT UNSIGNED NOT NULL,
    asset_type ENUM('stock','crypto','gold') NOT NULL,
    asset_symbol VARCHAR(20) NOT NULL,
    alert_type ENUM('price_threshold','pct_change','both') NOT NULL,
    price_lower_usd DECIMAL(20,8) NULL,
    price_upper_usd DECIMAL(20,8) NULL,
    pct_change_threshold DECIMAL(5,2) NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_asset_subscriptions_user_symbol (user_id, asset_symbol),
    CONSTRAINT fk_asset_subscriptions_user_id FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
