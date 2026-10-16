-- #40: the currency a price_level rule's level is entered in. Existing rules
-- are USD rules.
ALTER TABLE asset_rules
    ADD COLUMN level_currency ENUM('USD', 'IDR') NOT NULL DEFAULT 'USD' AFTER level_usd;
