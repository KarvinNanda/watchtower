-- Manual rollback only: the card falls back to the USD fired price.
ALTER TABLE asset_rules DROP COLUMN triggered_price_idr;
