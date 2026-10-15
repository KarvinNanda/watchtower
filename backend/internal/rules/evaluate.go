package rules

import (
	"math"
	"time"
)

// Evaluate decides whether rule r fires for market m at time now. It never
// reads the clock or any external state; everything it needs is passed in.
func Evaluate(r Rule, m Market, now time.Time, opt Options) Outcome {
	switch r.Type {
	case TypePriceLevel:
		return evalPriceLevel(r, m, now, opt)
	case TypePctChange24h:
		return evalPctChange(r, m, now, opt)
	}
	return Outcome{Skip: SkipUnknownType}
}

// evalPriceLevel is edge-triggered: it fires when the price crosses the
// level, not on every run while the price stays beyond it (spec §5.2).
func evalPriceLevel(r Rule, m Market, now time.Time, opt Options) Outcome {
	beyond := isBeyond(r.Direction, m.PriceUSD, r.LevelUSD)
	if r.State.BaselinePending {
		return Outcome{Skip: SkipBaseline, Observed: Patch{ClearBaseline: true, LastBeyond: boolPtr(beyond)}}
	}

	last := r.State.LastBeyond
	if !beyond {
		if last == nil || *last {
			return Outcome{Observed: Patch{LastBeyond: boolPtr(false)}}
		}
		return Outcome{}
	}
	if last != nil && *last {
		return Outcome{} // this crossing was already reported
	}
	// A crossing held by cooldown leaves last_beyond untouched, so it is
	// delivered once the cooldown ends if the price is still beyond.
	if inCooldown(r.State, m.PriceUSD, now, opt) {
		return Outcome{Skip: SkipCooldown}
	}
	return Outcome{
		Fire:    true,
		Trigger: triggerFor(r),
		OnSent:  Patch{LastBeyond: boolPtr(true), Alert: alertMark(now, m.PriceUSD, opt)},
	}
}

func evalPctChange(r Rule, m Market, now time.Time, opt Options) Outcome {
	if m.ChangePct24h == nil {
		return Outcome{Skip: SkipDataUnavailable}
	}
	if r.State.BaselinePending {
		return Outcome{Skip: SkipBaseline, Observed: Patch{ClearBaseline: true}}
	}
	if math.Abs(*m.ChangePct24h) <= r.Pct {
		return Outcome{}
	}
	if inCooldown(r.State, m.PriceUSD, now, opt) {
		return Outcome{Skip: SkipCooldown}
	}
	return Outcome{Fire: true, Trigger: triggerFor(r), OnSent: Patch{Alert: alertMark(now, m.PriceUSD, opt)}}
}

// isBeyond reports whether price has reached level in direction d.
// Touching the level counts as reaching it.
func isBeyond(d Direction, price, level float64) bool {
	switch d {
	case DirectionAbove:
		return price >= level
	case DirectionBelow:
		return price <= level
	}
	return false
}

// inCooldown keeps today's per-symbol rule, now per rule: inside the
// cooldown window an alert is held unless the price moved at least
// AlertPriceMovePct since the last alert.
func inCooldown(s State, price float64, now time.Time, opt Options) bool {
	if opt.SkipCooldown || s.CooldownUntil == nil || !now.Before(*s.CooldownUntil) {
		return false
	}
	if s.LastAlertedPriceUSD == nil || *s.LastAlertedPriceUSD == 0 {
		return true
	}
	movePct := math.Abs(price-*s.LastAlertedPriceUSD) / *s.LastAlertedPriceUSD * 100
	return movePct < opt.AlertPriceMovePct
}

func alertMark(now time.Time, price float64, opt Options) *AlertMark {
	until := now.Add(time.Duration(opt.CooldownHours) * time.Hour)
	return &AlertMark{At: now, PriceUSD: price, CooldownUntil: &until}
}

func triggerFor(r Rule) Trigger {
	return Trigger{Type: r.Type, Direction: r.Direction, Intent: r.Intent, LevelUSD: r.LevelUSD, Pct: r.Pct}
}

func boolPtr(v bool) *bool        { return &v }
func floatPtr(v float64) *float64 { return &v }
