INSERT INTO asset_rules
    (user_id, asset_type, asset_symbol, rule_type, direction, level_usd, pct, is_active, baseline_pending, created_at)
SELECT user_id, asset_type, asset_symbol, 'price_level', 'below', price_lower_usd, NULL, MAX(is_active), TRUE, MIN(created_at)
FROM asset_subscriptions
WHERE alert_type IN ('price_threshold', 'both') AND price_lower_usd IS NOT NULL
GROUP BY user_id, asset_type, asset_symbol, price_lower_usd
UNION ALL
SELECT user_id, asset_type, asset_symbol, 'price_level', 'above', price_upper_usd, NULL, MAX(is_active), TRUE, MIN(created_at)
FROM asset_subscriptions
WHERE alert_type IN ('price_threshold', 'both') AND price_upper_usd IS NOT NULL
GROUP BY user_id, asset_type, asset_symbol, price_upper_usd
UNION ALL
SELECT user_id, asset_type, asset_symbol, 'pct_change_24h', NULL, NULL, pct_change_threshold, MAX(is_active), TRUE, MIN(created_at)
FROM asset_subscriptions
WHERE alert_type IN ('pct_change', 'both') AND pct_change_threshold IS NOT NULL
GROUP BY user_id, asset_type, asset_symbol, pct_change_threshold;
