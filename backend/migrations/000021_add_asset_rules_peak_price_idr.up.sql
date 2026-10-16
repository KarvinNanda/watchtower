-- #58: the peak of a gold trailing stop, tracked on the native rupiah price.
ALTER TABLE asset_rules
    ADD COLUMN peak_price_idr DECIMAL(24,8) NULL AFTER peak_since;
