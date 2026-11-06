package alertmsg

import (
	"fmt"
	"math"
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

	// #58: an ANTAM trailing stop is tracked and reported in rupiah.
	antamTrailing := []AssetAlert{{RuleID: 9, Symbol: "ANTAM", PriceUSD: 125.65, NativeIDR: 2250000, ChangePct: f64(-1.1),
		Trigger: rules.Trigger{Type: rules.TypeTrailingStop, Pct: 10, Currency: rules.CurrencyIDR, PeakPriceIDR: 2500000}}}

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
		{"asset_id_antam_trailing_idr", "id", testRate, antamTrailing},
		{"asset_en_antam_trailing_idr", "en", testRate, antamTrailing},
		{"asset_id_antam_trailing_idr_no_rate", "id", Rate{}, antamTrailing},
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

func TestIDRAmount(t *testing.T) {
	assert.Equal(t, "Rp 2.500.000 ≈ $139.60", idrAmount(testRate, 2500000))
	assert.Equal(t, "Rp 2.500.000", idrAmount(Rate{}, 2500000))
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

func TestStockSessionIsToday(t *testing.T) {
	utc := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		require.NoError(t, err)
		return v
	}
	ptr := func(v time.Time) *time.Time { return &v }
	cases := []struct {
		name    string
		session *time.Time
		at      time.Time
		want    bool
	}{
		{"unknown session keeps today", nil, utc("2026-11-06T06:41:00Z"), true},
		{"weekday before the open", ptr(utc("2026-11-05T14:30:00Z")), utc("2026-11-06T06:41:00Z"), false},
		{"during the session", ptr(utc("2026-11-06T14:30:00Z")), utc("2026-11-06T20:00:00Z"), true},
		{"friday evening in new york is still friday", ptr(utc("2026-11-06T14:30:00Z")), utc("2026-11-07T03:00:00Z"), true},
		{"saturday", ptr(utc("2026-11-06T14:30:00Z")), utc("2026-11-07T15:00:00Z"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, stockSessionIsToday(tc.session, tc.at))
		})
	}
}

func TestAssetMessages_StockLastSessionLabel(t *testing.T) {
	session := time.Date(2026, 11, 5, 14, 30, 0, 0, time.UTC)
	at := time.Date(2026, 11, 6, 6, 41, 0, 0, time.UTC) // Friday before the US open
	alert := AssetAlert{RuleID: 1, Symbol: "NVDA", AssetType: "stock", PriceUSD: 181.6, ChangePct: f64(-9.26),
		ChangePct7d: f64(-15.0051), SessionStart: &session, Trigger: rules.Trigger{Type: rules.TypePctChange24h, Pct: 5}}

	id := AssetMessages("id", at, testRate, []AssetAlert{alert})[0]
	assert.Contains(t, id, esc("📉 Sesi terakhir: -9.26% · 7 hari: -15.01%"))
	assert.NotContains(t, id, "Hari ini")
	assert.NotContains(t, id, "tutup kemarin")

	en := AssetMessages("en", at, testRate, []AssetAlert{alert})[0]
	assert.Contains(t, en, esc("📉 Last session: -9.26% · 7d: -15.01%"))
}

func TestChangeLine(t *testing.T) {
	cases := []struct {
		name, lang, typ string
		p24, p7         *float64
		want            string
		lastSession     bool
	}{
		{"stock id last session", "id", "stock", f64(-1.53), f64(2.1), "📉 Sesi terakhir: -1.53% · 7 hari: +2.10%", true},
		{"stock en last session", "en", "stock", f64(1.5), nil, "📈 Last session: +1.50%", true},
		{"crypto ignores last session", "en", "crypto", f64(1.5), nil, "📈 24h: +1.50%", true},
		{"crypto id both", "id", "crypto", f64(-1.8), f64(-3.0276), "📉 24 jam: -1.80% · 7 hari: -3.03%", false},
		{"crypto en both", "en", "crypto", f64(1.5), f64(2), "📈 24h: +1.50% · 7d: +2.00%", false},
		{"stock en today only", "en", "stock", f64(1.5), nil, "📈 Today: +1.50% (vs previous close)", false},
		{"stock id both", "id", "stock", f64(-1.53), f64(2.1), "📉 Hari ini: -1.53% (vs tutup kemarin) · 7 hari: +2.10%", false},
		{"gold id 7d only zero", "id", "gold", nil, f64(0), "📈 7 hari: +0.00%", false},
		{"empty type is crypto", "en", "", f64(1), nil, "📈 24h: +1.00%", false},
		{"first shown value is 7d", "en", "crypto", nil, f64(-1), "📉 7d: -1.00%", false},
		{"tiny negative rounds to zero", "en", "crypto", f64(-0.001), nil, "📈 24h: +0.00%", false},
		{"negative zero", "en", "crypto", f64(math.Copysign(0, -1)), nil, "📈 24h: +0.00%", false},
		{"NaN 24h is dropped", "id", "crypto", f64(math.NaN()), f64(-1.5), "📉 7 hari: -1.50%", false},
		{"Inf 7d is dropped", "en", "crypto", f64(1), f64(math.Inf(1)), "📈 24h: +1.00%", false},
		{"both nil", "id", "crypto", nil, nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, esc(tc.want), changeLine(tc.lang, tc.typ, tc.p24, tc.p7, tc.lastSession))
		})
	}
}

func TestWeek52Line(t *testing.T) {
	assert.Equal(t, esc("📏 52-week: high $90,000.00 (-9.2%) · low $49,000.00 (+66.8%)"),
		week52Line(81714, f64(90000), f64(49000)))
	assert.Equal(t, esc("📏 52-week: high $95,000.00 (+0.0%) · low $49,000.00 (+93.9%)"),
		week52Line(95000, f64(90000), f64(49000)), "a new high counts as the high")
	assert.Equal(t, esc("📏 52-week: high $90,000.00 (-44.4%) · low $50,000.00 (+0.0%)"),
		week52Line(50000, f64(90000), f64(60000)), "a new low counts as the low")
	assert.Empty(t, week52Line(81714, nil, f64(49000)))
	assert.Empty(t, week52Line(81714, f64(90000), nil))
	assert.Empty(t, week52Line(81714, f64(0), f64(49000)))
	assert.Empty(t, week52Line(81714, f64(90000), f64(0)))
}

func TestAgeLine(t *testing.T) {
	at := time.Date(2026, 10, 15, 9, 30, 0, 0, time.UTC)
	assert.Empty(t, ageLine("id", at, at.Add(-15*time.Minute)), "exactly the threshold is not shown")
	assert.Equal(t, esc("⏳ data dari 16 menit lalu"), ageLine("id", at, at.Add(-16*time.Minute)))
	assert.Equal(t, esc("⏳ data dari 2 jam lalu"), ageLine("id", at, at.Add(-2*time.Hour)))
	assert.Equal(t, esc("⏳ data from 2 hour(s) ago"), ageLine("en", at, at.Add(-2*time.Hour)))
	assert.Empty(t, ageLine("id", at, time.Time{}), "unknown fetch time")
}

func TestAlertDistanceTail(t *testing.T) {
	assert.Equal(t, " · sudah lewat 2.1%", alertDistanceTail("id", rules.Distance{Kind: rules.DistanceLevel, Passed: true, Pct: -2.1}))
	assert.Equal(t, " · past by 2.1%", alertDistanceTail("en", rules.Distance{Kind: rules.DistanceLevel, Passed: true, Pct: -2.1}))
	assert.Equal(t, " · jarak +2.1%", alertDistanceTail("id", rules.Distance{Kind: rules.DistanceLevel, Pct: 2.1}))
	assert.Empty(t, alertDistanceTail("id", rules.Distance{Kind: rules.DistanceChange24h}))
	assert.Empty(t, alertDistanceTail("id", rules.Distance{Kind: rules.DistanceUnavailable}))
}

func TestAssetMessages_EnrichedGolden(t *testing.T) {
	at := jakartaAt(t)
	fresh := at.Add(-3 * time.Minute)
	old := at.Add(-23 * time.Minute)
	// A fired level rule is always passed, with Pct = (level - price) / price * 100
	// as levelDistance computes it.
	btcPassed := rules.Distance{Kind: rules.DistanceLevel, Passed: true, Pct: (80000 - 78000.0) / 78000.0 * 100}
	aaplPassed := rules.Distance{Kind: rules.DistanceLevel, Passed: true, Pct: (194.5 - 198.6) / 198.6 * 100}
	crypto := AssetAlert{RuleID: 1, Symbol: "BTC", AssetType: "crypto", PriceUSD: 78000, ChangePct: f64(-1.8), ChangePct7d: f64(-3.0276),
		FetchedAt: old, Week52High: f64(90000), Week52Low: f64(49000), Distance: btcPassed,
		Trigger: rules.Trigger{Type: rules.TypePriceLevel, Direction: rules.DirectionBelow, Intent: rules.IntentBuyTarget, LevelUSD: 80000}}
	stock := AssetAlert{RuleID: 2, Symbol: "AAPL", AssetType: "stock", PriceUSD: 198.6, ChangePct: f64(1.53),
		FetchedAt: fresh, Week52High: f64(200), Week52Low: f64(150), Distance: aaplPassed,
		Trigger: rules.Trigger{Type: rules.TypePriceLevel, Direction: rules.DirectionAbove, LevelUSD: 194.5}}
	gold := AssetAlert{RuleID: 3, Symbol: "ANTAM", AssetType: "gold", PriceUSD: 133.58, NativeIDR: 2392157, ChangePct: f64(0.42), ChangePct7d: f64(1.1),
		FetchedAt: fresh, Distance: rules.Distance{Kind: rules.DistanceChange24h},
		Trigger: rules.Trigger{Type: rules.TypePctChange24h, Pct: 0.4}}
	alerts := []AssetAlert{crypto, stock, gold}
	for _, lang := range []string{"id", "en"} {
		t.Run(lang, func(t *testing.T) {
			assertGolden(t, "asset_"+lang+"_enriched", AssetMessages(lang, at, testRate, alerts))
		})
	}
}
