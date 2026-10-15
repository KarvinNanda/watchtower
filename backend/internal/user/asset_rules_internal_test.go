package user

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sp(v string) *string   { return &v }
func fp(v float64) *float64 { return &v }

func TestNormalizeAssetRule(t *testing.T) {
	base := func(ruleType string) AssetRuleInput {
		return AssetRuleInput{AssetType: "crypto", AssetSymbol: " btc ", RuleType: ruleType}
	}
	with := func(in AssetRuleInput, mut func(*AssetRuleInput)) AssetRuleInput { mut(&in); return in }

	cases := []struct {
		name string
		in   AssetRuleInput
		err  error
	}{
		{"level with intent", with(base("price_level"), func(i *AssetRuleInput) { i.Intent, i.LevelUSD = sp("take_profit"), fp(120000) }), nil},
		{"level with matching direction", with(base("price_level"), func(i *AssetRuleInput) { i.Intent, i.Direction, i.LevelUSD = sp("stop_loss"), sp("below"), fp(85000) }), nil},
		{"level without intent needs direction", with(base("price_level"), func(i *AssetRuleInput) { i.LevelUSD = fp(1) }), ErrInvalidDirection},
		{"intent conflicts with direction", with(base("price_level"), func(i *AssetRuleInput) { i.Intent, i.Direction, i.LevelUSD = sp("take_profit"), sp("below"), fp(1) }), ErrIntentDirectionConflict},
		{"unknown intent", with(base("price_level"), func(i *AssetRuleInput) { i.Intent, i.LevelUSD = sp("buy_now"), fp(1) }), ErrInvalidIntent},
		{"level missing", with(base("price_level"), func(i *AssetRuleInput) { i.Direction = sp("above") }), ErrMissingLevel},
		{"level zero", with(base("price_level"), func(i *AssetRuleInput) { i.Direction, i.LevelUSD = sp("above"), fp(0) }), ErrMissingLevel},
		{"level with pct", with(base("price_level"), func(i *AssetRuleInput) { i.Direction, i.LevelUSD, i.Pct = sp("above"), fp(1), fp(5) }), ErrFieldNotAllowed},
		{"pct change ok", with(base("pct_change_24h"), func(i *AssetRuleInput) { i.Pct = fp(100) }), nil},
		{"pct change over 100", with(base("pct_change_24h"), func(i *AssetRuleInput) { i.Pct = fp(100.01) }), ErrInvalidPct},
		{"pct change with direction", with(base("pct_change_24h"), func(i *AssetRuleInput) { i.Pct, i.Direction = fp(5), sp("above") }), ErrFieldNotAllowed},
		{"trailing 100 is invalid", with(base("trailing_stop"), func(i *AssetRuleInput) { i.Pct = fp(100) }), ErrInvalidPct},
		{"trailing missing pct", base("trailing_stop"), ErrInvalidPct},
		{"week52 ok", with(base("week52_break"), func(i *AssetRuleInput) { i.Direction = sp("above") }), nil},
		{"week52 needs direction", base("week52_break"), ErrInvalidDirection},
		{"week52 not for gold", AssetRuleInput{AssetType: "gold", AssetSymbol: "ANTAM", RuleType: "week52_break", Direction: sp("above")}, ErrWeek52NotForGold},
		{"symbol of another type", AssetRuleInput{AssetType: "stock", AssetSymbol: "BTC", RuleType: "pct_change_24h", Pct: fp(5)}, ErrInvalidAssetSymbol},
		{"symbol not whitelisted", AssetRuleInput{AssetType: "stock", AssetSymbol: "BRK-B", RuleType: "pct_change_24h", Pct: fp(5)}, ErrInvalidAssetSymbol},
		{"unknown rule type", base("volume_spike"), ErrInvalidRuleType},
		{"unknown asset type", AssetRuleInput{AssetType: "bond", AssetSymbol: "BTC", RuleType: "pct_change_24h", Pct: fp(5)}, ErrInvalidAssetType},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalizeAssetRule(tc.in)
			assert.ErrorIs(t, err, tc.err)
			if tc.err == nil {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNormalizeAssetRule_DerivesDirectionAndCleansSymbol(t *testing.T) {
	out, err := normalizeAssetRule(AssetRuleInput{AssetType: "crypto", AssetSymbol: " btc ", RuleType: "price_level", Intent: sp("buy_target"), LevelUSD: fp(90000)})
	require.NoError(t, err)
	assert.Equal(t, "BTC", out.AssetSymbol)
	require.NotNil(t, out.Direction)
	assert.Equal(t, "below", *out.Direction)
}
