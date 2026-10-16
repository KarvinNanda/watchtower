-- Manual rollback only: drops every IDR level. Deactivate or delete the
-- level_currency = 'IDR' rules first: afterwards their level_usd is NULL,
-- which the scheduler reads as $0, so an "above" rule would fire once.
ALTER TABLE asset_rules DROP COLUMN level_idr;
