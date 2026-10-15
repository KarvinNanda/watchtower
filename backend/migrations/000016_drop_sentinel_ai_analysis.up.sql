-- DeepSeek analysis was removed in Spec 1; nothing reads or writes these
-- columns since. Dropping them deletes any old analysis text (approved, #5).
ALTER TABLE sentinel_seen_items
    DROP COLUMN ai_analysis_id,
    DROP COLUMN ai_analysis_en;
