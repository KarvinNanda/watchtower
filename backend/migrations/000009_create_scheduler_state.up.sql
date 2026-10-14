CREATE TABLE IF NOT EXISTS scheduler_state (
    pipeline VARCHAR(20) NOT NULL,
    last_started_at TIMESTAMP NULL,
    last_finished_at TIMESTAMP NULL,
    last_status VARCHAR(20) NULL,
    PRIMARY KEY (pipeline)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
