package alertmsg

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/rules"
)

func f64(v float64) *float64 { return &v }

func jakartaAt(t *testing.T) time.Time {
	t.Helper()
	jkt, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)
	return time.Date(2026, 10, 15, 9, 30, 0, 0, jkt)
}

// testRate was last updated 00:02:31 UTC = 07:02 WIB.
var testRate = Rate{Value: 17908, UpdatedAt: time.Date(2026, 10, 15, 0, 2, 31, 0, time.UTC)}

// basicAlerts is out of order on purpose: ETH first, BTC rule 9 before 7.
func basicAlerts() []AssetAlert {
	return []AssetAlert{
		{RuleID: 3, Symbol: "ETH", PriceUSD: 2950.10, Trigger: rules.Trigger{
			Type: rules.TypePriceLevel, Direction: rules.DirectionBelow, LevelUSD: 3000}},
		{RuleID: 9, Symbol: "BTC", PriceUSD: 81714, ChangePct: f64(-1.8), Trigger: rules.Trigger{
			Type: rules.TypeWeek52Break, Direction: rules.DirectionAbove, PrevRefUSD: 79500}},
		{RuleID: 7, Symbol: "BTC", PriceUSD: 81714, ChangePct: f64(-1.8), Trigger: rules.Trigger{
			Type: rules.TypePriceLevel, Direction: rules.DirectionAbove, Intent: rules.IntentTakeProfit, LevelUSD: 80000}},
	}
}

func TestAssetMessages_Golden(t *testing.T) {
	at := jakartaAt(t)
	antam := AssetAlert{RuleID: 1, Symbol: "ANTAM", PriceUSD: 133.58, NativeIDR: 2392157, ChangePct: f64(0.42),
		Trigger: rules.Trigger{Type: rules.TypePriceLevel, Direction: rules.DirectionBelow, Intent: rules.IntentBuyTarget, LevelUSD: 135}}

	var allTypes []AssetAlert
	for i, trig := range []rules.Trigger{
		{Type: rules.TypePriceLevel, Direction: rules.DirectionBelow, Intent: rules.IntentBuyTarget, LevelUSD: 0.00001},
		{Type: rules.TypePriceLevel, Direction: rules.DirectionAbove, Intent: rules.IntentTakeProfit, LevelUSD: 0.00002},
		{Type: rules.TypePriceLevel, Direction: rules.DirectionBelow, Intent: rules.IntentStopLoss, LevelUSD: 0.000005},
		{Type: rules.TypePriceLevel, Direction: rules.DirectionAbove, LevelUSD: 0.000012},
		{Type: rules.TypePriceLevel, Direction: rules.DirectionBelow, LevelUSD: 0.000013},
		{Type: rules.TypePctChange24h, Pct: 7.5},
		{Type: rules.TypeTrailingStop, Pct: 10, PeakPriceUSD: 0.000015},
		{Type: rules.TypeWeek52Break, Direction: rules.DirectionAbove, PrevRefUSD: 0.0000123},
		{Type: rules.TypeWeek52Break, Direction: rules.DirectionBelow, PrevRefUSD: 0.0000099},
		{Type: "mystery"},
	} {
		allTypes = append(allTypes, AssetAlert{RuleID: uint64(i + 1), Symbol: "SHIB", PriceUSD: 0.00001234, ChangePct: f64(12.3456), Trigger: trig})
	}

	var many []AssetAlert
	for i, s := range []string{"SOL", "ADA", "BTC", "ETH", "XRP", "DOGE", "AAPL"} {
		many = append(many, AssetAlert{RuleID: uint64(i + 1), Symbol: s, PriceUSD: 100, Trigger: rules.Trigger{
			Type: rules.TypePctChange24h, Pct: 5}})
	}

	var split []AssetAlert
	for i := 0; i < 40; i++ {
		split = append(split, AssetAlert{RuleID: uint64(i + 1), Symbol: fmt.Sprintf("SYM%02d", i), PriceUSD: 1234.5,
			ChangePct: f64(-2.5), Trigger: rules.Trigger{Type: rules.TypeTrailingStop, Pct: 5, PeakPriceUSD: 1300}})
	}

	// #63: one symbol with more fired rules than fit in one message.
	var oversized []AssetAlert
	for i := 0; i < 60; i++ {
		oversized = append(oversized, AssetAlert{RuleID: uint64(i + 1), Symbol: "BTC", PriceUSD: 81714, ChangePct: f64(1.2),
			Trigger: rules.Trigger{Type: rules.TypeTrailingStop, Pct: float64(i%9 + 1), PeakPriceUSD: 90000}})
	}

	idrLevels := []AssetAlert{
		{RuleID: 1, Symbol: "ANTAM", PriceUSD: 130.9, NativeIDR: 2344000, ChangePct: f64(-0.3), Trigger: rules.Trigger{
			Type: rules.TypePriceLevel, Direction: rules.DirectionBelow, Intent: rules.IntentBuyTarget, Currency: rules.CurrencyIDR, LevelIDR: 2350000}},
		{RuleID: 2, Symbol: "BTC", PriceUSD: 81714, ChangePct: f64(1.2), Trigger: rules.Trigger{
			Type: rules.TypePriceLevel, Direction: rules.DirectionAbove, Currency: rules.CurrencyIDR, LevelIDR: 1400000000}},
	}

	cases := []struct {
		name   string
		lang   string
		rate   Rate
		alerts []AssetAlert
	}{
		{"asset_id_basic", "id", testRate, basicAlerts()},
		{"asset_en_basic", "en", testRate, basicAlerts()},
		{"asset_id_rate_unavailable", "id", Rate{}, append(basicAlerts(), antam)},
		{"asset_en_rate_unavailable", "en", Rate{}, append(basicAlerts(), antam)},
		{"asset_en_antam_native_idr", "en", testRate, []AssetAlert{antam}},
		{"asset_id_antam_native_idr", "id", testRate, []AssetAlert{antam}},
		{"asset_en_all_trigger_types", "en", Rate{Value: 17908}, allTypes},
		{"asset_id_all_trigger_types", "id", Rate{Value: 17908}, allTypes},
		{"asset_en_many_symbols", "en", testRate, many},
		{"asset_id_many_symbols", "id", testRate, many},
		{"asset_id_split", "id", testRate, split},
		{"asset_en_split", "en", testRate, split},
		{"asset_id_oversized_symbol", "id", testRate, oversized},
		{"asset_id_idr_levels", "id", testRate, idrLevels},
		{"asset_en_idr_levels", "en", testRate, idrLevels},
		{"asset_id_idr_rate_unavailable", "id", Rate{}, idrLevels},
		{"asset_en_idr_rate_unavailable", "en", Rate{}, idrLevels},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertGolden(t, tc.name, AssetMessages(tc.lang, at, tc.rate, tc.alerts))
		})
	}
}

func TestLevelAmount(t *testing.T) {
	usd := rules.Trigger{Type: rules.TypePriceLevel, LevelUSD: 100}
	idr := rules.Trigger{Type: rules.TypePriceLevel, Currency: rules.CurrencyIDR, LevelIDR: 1350000}
	assert.Equal(t, amount(testRate, 100), levelAmount(testRate, usd), "USD rules are unchanged")
	assert.Equal(t, "Rp 1.350.000 ≈ $75.39", levelAmount(testRate, idr))
	assert.Equal(t, "Rp 1.350.000", levelAmount(Rate{}, idr), "no USD equivalent without a real rate")
}

func TestAssetMessages_EmptyIsNil(t *testing.T) {
	assert.Nil(t, AssetMessages("id", jakartaAt(t), testRate, nil))
}

func TestAssetMessages_OneBlockPerSymbol(t *testing.T) {
	msgs := AssetMessages("en", jakartaAt(t), testRate, basicAlerts())
	require.Len(t, msgs, 1)
	assert.Equal(t, 1, strings.Count(msgs[0], "📊 *BTC*"), "#23: two BTC rules share one block")
	assert.Less(t, strings.Index(msgs[0], "🎯"), strings.Index(msgs[0], "🆕"), "triggers ordered by rule ID")
	assert.Less(t, strings.Index(msgs[0], "*BTC*"), strings.Index(msgs[0], "*ETH*"), "symbols alphabetical")
}

func TestAssetMessages_HeaderCapsSymbols(t *testing.T) {
	var alerts []AssetAlert
	for i, s := range []string{"G", "F", "E", "D", "C", "B", "A"} {
		alerts = append(alerts, AssetAlert{RuleID: uint64(i), Symbol: s, PriceUSD: 1, Trigger: rules.Trigger{Type: rules.TypePctChange24h, Pct: 1}})
	}
	msgs := AssetMessages("en", jakartaAt(t), testRate, alerts)
	assert.True(t, strings.HasPrefix(msgs[0], "🔔 *Asset alert* · A, B, C, D, E, \\+2 \\(7 triggers\\)\n"), msgs[0])
}

func TestAssetMessages_FallbackFreeWhenRateUnavailable(t *testing.T) {
	msgs := AssetMessages("en", jakartaAt(t), Rate{}, basicAlerts())
	assert.Contains(t, msgs[0], "💱 USD/IDR rate unavailable")
	assert.NotContains(t, msgs[0], "Rp", "no converted IDR without a real rate")
}
