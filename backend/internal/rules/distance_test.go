package rules

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The cases mirror frontend/src/utils/ruleDistance.test.js; keep the two in
// sync (#7).

func priceRule(dir Direction, level float64) Rule {
	return Rule{ID: 1, Type: TypePriceLevel, Direction: dir, LevelUSD: level}
}

func TestDistanceTo_PriceLevel(t *testing.T) {
	m := Market{PriceUSD: 100}

	d := DistanceTo(priceRule(DirectionAbove, 110), m)
	assert.Equal(t, Distance{Kind: DistanceLevel, Level: 110, Pct: 10}, d)

	assert.True(t, DistanceTo(priceRule(DirectionAbove, 100), m).Passed, "exactly at the level counts as passed")

	below := DistanceTo(priceRule(DirectionBelow, 95), m)
	assert.InDelta(t, -5, below.Pct, 1e-9)
	assert.False(t, below.Passed)
	assert.True(t, DistanceTo(priceRule(DirectionBelow, 95), Market{PriceUSD: 94}).Passed)

	assert.True(t, DistanceTo(priceRule(DirectionAbove, 103), m).Near)
	assert.False(t, DistanceTo(priceRule(DirectionAbove, 103.5), m).Near)
	assert.False(t, DistanceTo(priceRule(DirectionAbove, 99), m).Near, "passed is never near")
}

func TestDistanceTo_IDRLevel(t *testing.T) {
	r := Rule{ID: 9, Type: TypePriceLevel, Direction: DirectionBelow, Intent: IntentBuyTarget, Currency: CurrencyIDR, LevelIDR: 2350000}
	idr := 2392157.0

	d := DistanceTo(r, Market{PriceUSD: 130, PriceIDR: &idr})
	assert.Equal(t, DistanceLevel, d.Kind)
	assert.Equal(t, 2350000.0, d.Level)
	assert.InDelta(t, -1.762, d.Pct, 0.01)
	assert.False(t, d.Passed)
	assert.True(t, d.Near)

	// Review focus 3: no IDR price means no distance, never one on PriceUSD.
	assert.Equal(t, Distance{Kind: DistanceUnavailable}, DistanceTo(r, Market{PriceUSD: 130}))
}

func TestDistanceTo_TrailingStop(t *testing.T) {
	peak := 200.0
	r := Rule{ID: 2, Type: TypeTrailingStop, Pct: 10, State: State{PeakPriceUSD: &peak}}

	d := DistanceTo(r, Market{PriceUSD: 190})
	assert.Equal(t, DistanceLevel, d.Kind)
	assert.InDelta(t, 180, d.Level, 1e-9)
	assert.Equal(t, 200.0, d.PeakUSD)
	assert.InDelta(t, -5.263, d.Pct, 0.01)
	assert.False(t, d.Passed)
	assert.True(t, DistanceTo(r, Market{PriceUSD: 180}).Passed)

	r.State.PeakPriceUSD = nil
	assert.Equal(t, Distance{Kind: DistanceWaitingPeak}, DistanceTo(r, Market{PriceUSD: 190}))
}

func TestDistanceTo_Change24h(t *testing.T) {
	change := -2.1
	r := Rule{ID: 3, Type: TypePctChange24h, Pct: 5}
	assert.Equal(t, Distance{Kind: DistanceChange24h, Current: &change, Threshold: 5}, DistanceTo(r, Market{PriceUSD: 100, ChangePct24h: &change}))
	assert.Nil(t, DistanceTo(r, Market{PriceUSD: 100}).Current)
}

func TestDistanceTo_NoDistance(t *testing.T) {
	assert.Equal(t, Distance{Kind: DistanceNone}, DistanceTo(Rule{Type: TypeWeek52Break, Direction: DirectionAbove}, Market{PriceUSD: 100}))
	assert.Equal(t, Distance{Kind: DistanceUnavailable}, DistanceTo(priceRule(DirectionAbove, 110), Market{PriceUSD: 0}))
}

func TestTriggerOf(t *testing.T) {
	r := Rule{ID: 4, Type: TypePriceLevel, Direction: DirectionBelow, Intent: IntentStopLoss, LevelUSD: 95, Currency: CurrencyUSD}
	assert.Equal(t, Trigger{Type: TypePriceLevel, Direction: DirectionBelow, Intent: IntentStopLoss, LevelUSD: 95, Currency: CurrencyUSD}, TriggerOf(r))
}
