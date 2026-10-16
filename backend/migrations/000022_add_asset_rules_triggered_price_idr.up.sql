-- #58: the rupiah price at which a gold trailing stop fired.
ALTER TABLE asset_rules
    ADD COLUMN triggered_price_idr DECIMAL(24,8) NULL AFTER triggered_price_usd;
