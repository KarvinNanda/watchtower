-- Manual rollback only: gold trailing stops fall back to their last USD peak.
ALTER TABLE asset_rules DROP COLUMN peak_price_idr;
