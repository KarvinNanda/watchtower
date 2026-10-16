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

func reportSymbols() []ReportSymbol {
	return []ReportSymbol{
		{Symbol: "BTC", PriceUSD: 82212, ChangePct: f64(0.7), Rules: []ReportRule{
			{RuleID: 3, Trigger: rules.Trigger{Type: rules.TypeTrailingStop, Pct: 10},
				Distance: rules.Distance{Kind: rules.DistanceLevel, Level: 73990.8, PeakUSD: 82212, Pct: -10}},
			{RuleID: 2, Trigger: rules.Trigger{Type: rules.TypePriceLevel, Direction: rules.DirectionAbove, Currency: rules.CurrencyIDR, LevelIDR: 1600000000},
				Distance: rules.Distance{Kind: rules.DistanceLevel, Level: 1600000000, Pct: 8.43}},
			{RuleID: 4, Trigger: rules.Trigger{Type: rules.TypePctChange24h, Pct: 5},
				Distance: rules.Distance{Kind: rules.DistanceChange24h, Current: f64(0.7), Threshold: 5}},
			{RuleID: 5, Trigger: rules.Trigger{Type: rules.TypeWeek52Break, Direction: rules.DirectionAbove},
				Distance: rules.Distance{Kind: rules.DistanceNone}},
		}},
		{Symbol: "ANTAM", PriceUSD: 133.76, NativeIDR: 2400902, ChangePct: f64(0.3), Rules: []ReportRule{
			{RuleID: 1, Trigger: rules.Trigger{Type: rules.TypePriceLevel, Direction: rules.DirectionBelow, Intent: rules.IntentBuyTarget, Currency: rules.CurrencyIDR, LevelIDR: 2350000},
				Distance: rules.Distance{Kind: rules.DistanceLevel, Level: 2350000, Pct: -2.12, Near: true}},
			{RuleID: 6, Trigger: rules.Trigger{Type: rules.TypePriceLevel, Direction: rules.DirectionAbove, Intent: rules.IntentTakeProfit, LevelUSD: 130},
				Distance: rules.Distance{Kind: rules.DistanceLevel, Level: 130, Pct: -2.81, Passed: true}},
		}},
		{Symbol: "AAPL", Unavailable: true},
	}
}

func TestAssetReportMessages_Golden(t *testing.T) {
	at := jakartaAt(t)
	staleSince := at.Add(-2 * time.Hour)
	stale := []ReportSymbol{
		{Symbol: "ETH", PriceUSD: 2439.01, ChangePct: f64(-1.5), StaleSince: &staleSince, Rules: []ReportRule{
			{RuleID: 7, Trigger: rules.Trigger{Type: rules.TypeTrailingStop, Pct: 8},
				Distance: rules.Distance{Kind: rules.DistanceWaitingPeak}},
			{RuleID: 8, Trigger: rules.Trigger{Type: rules.TypePriceLevel, Direction: rules.DirectionAbove, Currency: rules.CurrencyIDR, LevelIDR: 50000000},
				Distance: rules.Distance{Kind: rules.DistanceUnavailable}},
		}},
	}
	var split []ReportSymbol
	for i := 0; i < 40; i++ {
		split = append(split, ReportSymbol{Symbol: fmt.Sprintf("SYM%02d", i), PriceUSD: 1234.5, ChangePct: f64(-2.5), Rules: []ReportRule{
			{RuleID: uint64(i + 1), Trigger: rules.Trigger{Type: rules.TypeTrailingStop, Pct: 5},
				Distance: rules.Distance{Kind: rules.DistanceLevel, Level: 1235, PeakUSD: 1300, Pct: 0.04, Passed: true}},
		}})
	}

	cases := []struct {
		name    string
		lang    string
		rate    Rate
		symbols []ReportSymbol
	}{
		{"report_id_basic", "id", testRate, reportSymbols()},
		{"report_en_basic", "en", testRate, reportSymbols()},
		{"report_id_rate_unavailable", "id", Rate{}, reportSymbols()},
		{"report_en_rate_unavailable", "en", Rate{}, reportSymbols()},
		{"report_id_stale", "id", testRate, stale},
		{"report_en_split", "en", testRate, split},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertGolden(t, tc.name, AssetReportMessages(tc.lang, at, tc.rate, tc.symbols))
		})
	}
}

func TestAssetReportMessages_EmptyIsNil(t *testing.T) {
	assert.Nil(t, AssetReportMessages("id", jakartaAt(t), testRate, nil))
}

func TestAssetReportMessages_Content(t *testing.T) {
	msgs := AssetReportMessages("id", jakartaAt(t), testRate, reportSymbols())
	require.Len(t, msgs, 1)
	m := msgs[0]
	assert.Contains(t, m, "Laporan status aset")
	assert.Contains(t, m, `AAPL, ANTAM, BTC \(6 rule\)`, "symbols sorted, rules counted")
	assert.Contains(t, m, `jarak \-2\.1% \(dekat\)`)
	assert.Contains(t, m, "sudah lewat")
	assert.Contains(t, m, "harga tidak tersedia")
	assert.Less(t, strings.Index(m, "*AAPL*"), strings.Index(m, "*ANTAM*"))
	assert.Less(t, strings.Index(m, "1\\.600\\.000\\.000"), strings.Index(m, "Trailing stop"), "rules by ID: 2 before 3")
}
