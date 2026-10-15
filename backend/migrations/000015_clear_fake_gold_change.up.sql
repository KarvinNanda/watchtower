-- Before 000013, gold rows stored an unknown 24h change as 0, which the UI
-- and alerts showed as "0.00%". Mark it unknown until the next fetch (#30).
UPDATE market_cache SET change_pct_24h = NULL WHERE source = 'harga-emas.org';
