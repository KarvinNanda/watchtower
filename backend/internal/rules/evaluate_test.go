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
