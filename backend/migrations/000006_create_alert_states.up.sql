CREATE TABLE IF NOT EXISTS alert_states (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    user_id BIGINT UNSIGNED NOT NULL,
    asset_symbol VARCHAR(20) NOT NULL,
    last_alert_type ENUM('lower','upper','pct_change') NULL,
    last_alerted_price_usd DECIMAL(20,8) NULL,
    last_alerted_at TIMESTAMP NULL,
    cooldown_until TIMESTAMP NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_alert_states_user_symbol (user_id, asset_symbol),
    CONSTRAINT fk_alert_states_user_id FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
