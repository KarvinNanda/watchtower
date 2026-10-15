-- XAU was listed as "Gold (USD)" but always returned the Antam per-gram
-- price, the same data as ANTAM (#24). XAU is no longer offered, so its
-- rules move to ANTAM; their alerts do not change.
UPDATE asset_rules SET asset_symbol = 'ANTAM' WHERE asset_symbol = 'XAU';
