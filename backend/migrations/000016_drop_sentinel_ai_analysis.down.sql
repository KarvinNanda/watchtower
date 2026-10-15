-- Restores the columns empty; the dropped analysis text cannot come back.
ALTER TABLE sentinel_seen_items
    ADD COLUMN ai_analysis_id TEXT NULL AFTER item_identifier,
    ADD COLUMN ai_analysis_en TEXT NULL AFTER ai_analysis_id;
