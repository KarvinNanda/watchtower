CREATE TABLE IF NOT EXISTS notification_logs (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    user_id BIGINT UNSIGNED NOT NULL,
    notif_type ENUM('asset','sentinel') NOT NULL,
    asset_symbol VARCHAR(20) NULL,
    keyword VARCHAR(100) NULL,
    content_summary TEXT NULL,
    sent_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    status ENUM('sent','failed') NOT NULL DEFAULT 'sent',
    PRIMARY KEY (id),
    KEY idx_notification_logs_user_sent_at (user_id, sent_at),
    CONSTRAINT fk_notification_logs_user_id FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
