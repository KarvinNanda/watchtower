CREATE TABLE IF NOT EXISTS sentinel_seen_items (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    source_type ENUM('cve','rss','github','cisa_kev','exploit_db','github_advisory') NOT NULL,
    item_identifier VARCHAR(255) NOT NULL,
    ai_analysis_id TEXT NULL,
    ai_analysis_en TEXT NULL,
    first_seen_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_sentinel_seen_items_source_identifier (source_type, item_identifier),
    KEY idx_sentinel_seen_items_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
