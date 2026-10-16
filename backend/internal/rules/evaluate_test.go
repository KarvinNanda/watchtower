package rules

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var now = time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)

func f(v float64) *float64          { return &v }
func b(v bool) *bool                { return &v }
func at(d time.Duration) *time.Time { t := now.Add(d); return &t }

var opts = Options{AlertPriceMovePct: 3, CooldownHours: 4}

func levelRule(dir Direction, level float64, st State) Rule {
	return Rule{ID: 1, Type: TypePriceLevel, Direction: dir, LevelUSD: level, State: st}
}

func market(price float64) Market { return Market{PriceUSD: price} }

func sentAlert(price float64) *AlertMark {
	return &AlertMark{At: now, PriceUSD: price, CooldownUntil: at(4 * time.Hour)}
}

func TestIntentDirection(t *testing.T) {
	d, ok := IntentDirection(IntentBuyTarget)
	assert.True(t, ok)
	assert.Equal(t, DirectionBelow, d)
	d, _ = IntentDirection(IntentStopLoss)
	assert.Equal(t, DirectionBelow, d)
	d, _ = IntentDirection(IntentTakeProfit)
	assert.Equal(t, DirectionAbove, d)
	_, ok = IntentDirection(IntentNone)
	assert.False(t, ok)
}

func TestPriceLevel(t *testing.T) {
	cooling := State{LastBeyond: b(false), LastAlertedPriceUSD: f(104), CooldownUntil: at(2 * time.Hour)}

	cases := []struct {
		name string
		rule Rule
		mkt  Market
		opt  Options
		want Outcome
	}{
		{"new rule already beyond fires once", levelRule(DirectionAbove, 100, State{}), market(105), opts,
			Outcome{Fire: true, Trigger: Trigger{Type: TypePriceLevel, Direction: DirectionAbove, LevelUSD: 100},
				OnSent: Patch{LastBeyond: b(true), Alert: sentAlert(105)}}},
		{"new rule not beyond records the side", levelRule(DirectionAbove, 100, State{}), market(95), opts,
			Outcome{Observed: Patch{LastBeyond: b(false)}}},
		{"crossing fires, touching the level counts", levelRule(DirectionAbove, 100, State{LastBeyond: b(false)}), market(100), opts,
			Outcome{Fire: true, Trigger: Trigger{Type: TypePriceLevel, Direction: DirectionAbove, LevelUSD: 100},
				OnSent: Patch{LastBeyond: b(true), Alert: sentAlert(100)}}},
		{"still below does nothing", levelRule(DirectionAbove, 100, State{LastBeyond: b(false)}), market(99.99), opts,
			Outcome{}},
		{"still beyond after an alert does nothing", levelRule(DirectionAbove, 100, State{LastBeyond: b(true)}), market(120), opts,
			Outcome{}},
		{"back below re-arms", levelRule(DirectionAbove, 100, State{LastBeyond: b(true)}), market(90), opts,
			Outcome{Observed: Patch{LastBeyond: b(false)}}},
		{"baseline records silently", levelRule(DirectionAbove, 100, State{BaselinePending: true}), market(105), opts,
			Outcome{Skip: SkipBaseline, Observed: Patch{ClearBaseline: true, LastBeyond: b(true)}}},
		{"cooldown holds the crossing without consuming it", levelRule(DirectionAbove, 100, cooling), market(105), opts,
			Outcome{Skip: SkipCooldown}},
		{"a big price move bypasses cooldown", levelRule(DirectionAbove, 100, State{LastBeyond: b(false), LastAlertedPriceUSD: f(100), CooldownUntil: at(2 * time.Hour)}), market(105), opts,
			Outcome{Fire: true, Trigger: Trigger{Type: TypePriceLevel, Direction: DirectionAbove, LevelUSD: 100},
				OnSent: Patch{LastBeyond: b(true), Alert: sentAlert(105)}}},
		{"manual run skips cooldown", levelRule(DirectionAbove, 100, cooling), market(105), Options{SkipCooldown: true, AlertPriceMovePct: 3, CooldownHours: 4},
			Outcome{Fire: true, Trigger: Trigger{Type: TypePriceLevel, Direction: DirectionAbove, LevelUSD: 100},
				OnSent: Patch{LastBeyond: b(true), Alert: sentAlert(105)}}},
		{"cooldown ending exactly now no longer holds", levelRule(DirectionAbove, 100, State{LastBeyond: b(false), LastAlertedPriceUSD: f(104), CooldownUntil: at(0)}), market(105), opts,
			Outcome{Fire: true, Trigger: Trigger{Type: TypePriceLevel, Direction: DirectionAbove, LevelUSD: 100},
				OnSent: Patch{LastBeyond: b(true), Alert: sentAlert(105)}}},
		{"below rule fires at the level", levelRule(DirectionBelow, 100, State{LastBeyond: b(false)}), market(100), opts,
			Outcome{Fire: true, Trigger: Trigger{Type: TypePriceLevel, Direction: DirectionBelow, LevelUSD: 100},
				OnSent: Patch{LastBeyond: b(true), Alert: sentAlert(100)}}},
		{"below rule ignores prices above", levelRule(DirectionBelow, 100, State{LastBeyond: b(false)}), market(101), opts,
			Outcome{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Evaluate(tc.rule, tc.mkt, now, tc.opt))
		})
	}
}

func TestPriceLevel_IntentIsCarriedIntoTrigger(t *testing.T) {
	r := Rule{Type: TypePriceLevel, Direction: DirectionAbove, Intent: IntentTakeProfit, LevelUSD: 120000}
	out := Evaluate(r, market(121000), now, opts)
	require.True(t, out.Fire)
	assert.Equal(t, IntentTakeProfit, out.Trigger.Intent)
}

func TestPctChange24h(t *testing.T) {
	pctRule := func(st State) Rule { return Rule{Type: TypePctChange24h, Pct: 5, State: st} }
	withChange := func(c float64) Market { return Market{PriceUSD: 100, ChangePct24h: f(c)} }

	cases := []struct {
		name string
		rule Rule
		mkt  Market
		want Outcome
	}{
		{"unknown change is skipped", pctRule(State{}), Market{PriceUSD: 100}, Outcome{Skip: SkipDataUnavailable}},
		{"unknown change keeps the baseline pending", pctRule(State{BaselinePending: true}), Market{PriceUSD: 100}, Outcome{Skip: SkipDataUnavailable}},
		{"exactly the threshold does not fire", pctRule(State{}), withChange(5), Outcome{}},
		{"a drop beyond the threshold fires", pctRule(State{}), withChange(-5.01),
			Outcome{Fire: true, Trigger: Trigger{Type: TypePctChange24h, Pct: 5}, OnSent: Patch{Alert: sentAlert(100)}}},
		{"baseline clears silently", pctRule(State{BaselinePending: true}), withChange(10),
			Outcome{Skip: SkipBaseline, Observed: Patch{ClearBaseline: true}}},
		{"cooldown holds", pctRule(State{LastAlertedPriceUSD: f(100), CooldownUntil: at(time.Hour)}), withChange(8),
			Outcome{Skip: SkipCooldown}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Evaluate(tc.rule, tc.mkt, now, opts))
		})
	}
}

func TestUnknownRuleType(t *testing.T) {
	assert.Equal(t, Outcome{Skip: SkipUnknownType}, Evaluate(Rule{Type: "volume_spike"}, market(1), now, opts))
}

func TestPatchIsEmpty(t *testing.T) {
	assert.True(t, Patch{}.IsEmpty())
	assert.False(t, Patch{ClearBaseline: true}.IsEmpty())
	assert.False(t, Patch{Alert: &AlertMark{}}.IsEmpty())
}

func TestTrailingStop(t *testing.T) {
	trail := func(st State) Rule { return Rule{ID: 2, Type: TypeTrailingStop, Pct: 10, State: st} }
	// tracked is a peak recorded since two days ago.
	tracked := func(peak float64) State { return State{PeakPriceUSD: f(peak), PeakSince: at(-48 * time.Hour)} }
	withHigh := func(price, high float64, since time.Duration) Market {
		return Market{PriceUSD: price, HighUSD: f(high), HighSince: at(since)}
	}
	fired := func(peak, price float64, observed Patch) Outcome {
		return Outcome{
			Fire:     true,
			Trigger:  Trigger{Type: TypeTrailingStop, Pct: 10, PeakPriceUSD: peak},
			Observed: observed,
			OnSent:   Patch{Fired: &FiredMark{At: now, PriceUSD: price}, Alert: &AlertMark{At: now, PriceUSD: price}},
		}
	}

	cases := []struct {
		name string
		rule Rule
		mkt  Market
		want Outcome
	}{
		{"first run records the peak and when tracking started", trail(State{}), market(100),
			Outcome{Observed: Patch{PeakPriceUSD: f(100), PeakSince: at(0)}}},
		{"a higher price raises the peak", trail(tracked(100)), market(110), Outcome{Observed: Patch{PeakPriceUSD: f(110)}}},
		{"a small dip does nothing", trail(tracked(100)), market(90.01), Outcome{}},
		{"exactly the drop fires", trail(tracked(100)), market(90), fired(100, 90, Patch{})},
		{"cooldown never holds a trailing stop",
			trail(State{PeakPriceUSD: f(100), PeakSince: at(-48 * time.Hour), LastAlertedPriceUSD: f(80), CooldownUntil: at(time.Hour)}),
			market(80), fired(100, 80, Patch{})},
		// #21: a spike between two runs counts once its window started after tracking did.
		{"a high after tracking started raises the peak", trail(tracked(100)), withHigh(100, 105, -24*time.Hour),
			Outcome{Observed: Patch{PeakPriceUSD: f(105)}}},
		{"a high after tracking started can fire the rule", trail(tracked(118)), withHigh(110, 125, -24*time.Hour),
			fired(125, 110, Patch{PeakPriceUSD: f(125)})},
		// The #21 example: created 10:00 (7 h ago), BTC hit 125k at 03:00, now 110k.
		{"a high from before tracking started is ignored", trail(State{PeakPriceUSD: f(118), PeakSince: at(-7 * time.Hour)}),
			withHigh(110, 125, -24*time.Hour), Outcome{}},
		// Market closed: /quote still returns the last session, which began before the rule.
		{"closed market: last session's high predates tracking", trail(State{PeakPriceUSD: f(118), PeakSince: at(-30 * time.Hour)}),
			withHigh(110, 125, -40*time.Hour), Outcome{}},
		{"legacy peak: start tracking now, ignore the high", trail(State{PeakPriceUSD: f(100)}), withHigh(95, 200, -time.Hour),
			Outcome{Observed: Patch{PeakSince: at(0)}}},
		{"an unknown high is ignored", trail(tracked(100)), Market{PriceUSD: 95, HighUSD: nil, HighSince: at(-time.Hour)}, Outcome{}},
		{"a zero high is ignored", trail(tracked(100)), withHigh(95, 0, -time.Hour), Outcome{}},
		{"a high without its window start is ignored", trail(tracked(100)), Market{PriceUSD: 95, HighUSD: f(200)}, Outcome{}},
		{"odd percentages still fire at the boundary",
			Rule{Type: TypeTrailingStop, Pct: 7.3, State: State{PeakPriceUSD: f(0.3), PeakSince: at(-48 * time.Hour)}}, market(0.3 * (1 - 0.073)),
			Outcome{Fire: true, Trigger: Trigger{Type: TypeTrailingStop, Pct: 7.3, PeakPriceUSD: 0.3},
				OnSent: Patch{Fired: &FiredMark{At: now, PriceUSD: 0.3 * (1 - 0.073)}, Alert: &AlertMark{At: now, PriceUSD: 0.3 * (1 - 0.073)}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Evaluate(tc.rule, tc.mkt, now, opts))
		})
	}
}

func TestPatchIsEmpty_PeakSince(t *testing.T) {
	assert.False(t, Patch{PeakSince: at(0)}.IsEmpty())
}

func TestWeek52Break(t *testing.T) {
	above := func(st State) Rule { return Rule{ID: 3, Type: TypeWeek52Break, Direction: DirectionAbove, State: st} }
	below := func(st State) Rule { return Rule{ID: 4, Type: TypeWeek52Break, Direction: DirectionBelow, State: st} }
	hi := func(price, high float64) Market { return Market{PriceUSD: price, Week52High: f(high)} }
	lo := func(price, low float64) Market { return Market{PriceUSD: price, Week52Low: f(low)} }

	cases := []struct {
		name string
		rule Rule
		mkt  Market
		want Outcome
	}{
		{"no 52-week data is skipped", above(State{}), market(100), Outcome{Skip: SkipDataUnavailable}},
		{"first run records the ref", above(State{}), hi(343, 348), Outcome{Observed: Patch{Week52RefUSD: f(348)}}},
		{"baseline records silently", above(State{BaselinePending: true}), hi(343, 348),
			Outcome{Skip: SkipBaseline, Observed: Patch{ClearBaseline: true, Week52RefUSD: f(348)}}},
		{"price above the previous high fires", above(State{Week52RefUSD: f(348)}), hi(349, 350),
			Outcome{Fire: true, Trigger: Trigger{Type: TypeWeek52Break, Direction: DirectionAbove, PrevRefUSD: 348},
				OnSent: Patch{Week52RefUSD: f(350), Alert: sentAlert(349)}}},
		{"unchanged ref writes nothing", above(State{Week52RefUSD: f(348)}), hi(343, 348), Outcome{}},
		{"an old high leaving the window lowers the ref", above(State{Week52RefUSD: f(126000)}), hi(110000, 114419),
			Outcome{Observed: Patch{Week52RefUSD: f(114419)}}},
		{"after the window rolls, a lower new high fires", above(State{Week52RefUSD: f(114419)}), hi(115000, 114419),
			Outcome{Fire: true, Trigger: Trigger{Type: TypeWeek52Break, Direction: DirectionAbove, PrevRefUSD: 114419},
				OnSent: Patch{Week52RefUSD: f(115000), Alert: sentAlert(115000)}}},
		{"a held break is consumed", above(State{Week52RefUSD: f(348), LastAlertedPriceUSD: f(347), CooldownUntil: at(time.Hour)}), hi(349, 350),
			Outcome{Skip: SkipCooldown, Observed: Patch{Week52RefUSD: f(350)}}},
		{"price below the previous low fires", below(State{Week52RefUSD: f(243)}), lo(241, 240),
			Outcome{Fire: true, Trigger: Trigger{Type: TypeWeek52Break, Direction: DirectionBelow, PrevRefUSD: 243},
				OnSent: Patch{Week52RefUSD: f(240), Alert: sentAlert(241)}}},
		{"a below rule ignores the high", below(State{}), hi(100, 120), Outcome{Skip: SkipDataUnavailable}},
		{"no direction is invalid", Rule{Type: TypeWeek52Break}, hi(1, 1), Outcome{Skip: SkipUnknownType}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Evaluate(tc.rule, tc.mkt, now, opts))
		})
	}
}

func idrRule(dir Direction, level float64, st State) Rule {
	return Rule{ID: 1, Type: TypePriceLevel, Direction: dir, Currency: CurrencyIDR, LevelIDR: level, State: st}
}

func marketIDR(usd, idr float64) Market { return Market{PriceUSD: usd, PriceIDR: f(idr)} }

func TestPriceLevel_IDR(t *testing.T) {
	idrTrigger := func(dir Direction, level float64) Trigger {
		return Trigger{Type: TypePriceLevel, Direction: dir, Currency: CurrencyIDR, LevelIDR: level}
	}
	cases := []struct {
		name string
		rule Rule
		mkt  Market
		want Outcome
	}{
		{"crossing above in IDR fires; alert mark keeps the USD price",
			idrRule(DirectionAbove, 1350000, State{LastBeyond: b(false)}), marketIDR(75.4, 1350000),
			Outcome{Fire: true, Trigger: idrTrigger(DirectionAbove, 1350000), OnSent: Patch{LastBeyond: b(true), Alert: sentAlert(75.4)}}},
		{"below in IDR fires",
			idrRule(DirectionBelow, 2350000, State{LastBeyond: b(false)}), marketIDR(130, 2349999),
			Outcome{Fire: true, Trigger: idrTrigger(DirectionBelow, 2350000), OnSent: Patch{LastBeyond: b(true), Alert: sentAlert(130)}}},
		{"compares IDR, not USD: a USD price above the number does not fire",
			idrRule(DirectionAbove, 1400000, State{LastBeyond: b(false)}), marketIDR(9999999, 1350000),
			Outcome{}},
		{"unknown IDR price is skipped with no state change",
			idrRule(DirectionAbove, 1350000, State{LastBeyond: b(false)}), Market{PriceUSD: 80},
			Outcome{Skip: SkipDataUnavailable}},
		{"unknown IDR price keeps the baseline pending",
			idrRule(DirectionAbove, 1350000, State{BaselinePending: true}), Market{PriceUSD: 80},
			Outcome{Skip: SkipDataUnavailable}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Evaluate(tc.rule, tc.mkt, now, opts))
		})
	}
}

func TestPriceLevel_USDRuleIgnoresIDRPrice(t *testing.T) {
	got := Evaluate(levelRule(DirectionAbove, 100, State{LastBeyond: b(false)}), marketIDR(105, 1), now, opts)
	require.True(t, got.Fire)
	assert.Equal(t, Trigger{Type: TypePriceLevel, Direction: DirectionAbove, LevelUSD: 100}, got.Trigger)
}
