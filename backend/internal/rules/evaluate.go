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
	case TypeTrailingStop:
		return evalTrailingStop(r, m, now)
	case TypeWeek52Break:
		return evalWeek52(r, m, now, opt)
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

func boolPtr(v bool) *bool           { return &v }
func floatPtr(v float64) *float64    { return &v }
func timePtr(v time.Time) *time.Time { return &v }

// pctEpsilon absorbs float rounding so that a drop of exactly Pct percent
// fires (e.g. 0.3 * (1 - 0.073) must count as a 7.3% drop).
const pctEpsilon = 1e-9

// evalTrailingStop tracks the highest price since the rule was created or
// re-armed, and fires once when the price falls Pct percent below it. It
// also counts the source's high (#21), but only when that high's window
// starts at or after the time tracking started, so a spike from before
// creation or re-arm never fires the rule. It is one-shot: OnSent
// deactivates the rule. No cooldown.
func evalTrailingStop(r Rule, m Market, now time.Time) Outcome {
	price := m.PriceUSD
	if r.State.PeakPriceUSD == nil {
		return Outcome{Observed: Patch{PeakPriceUSD: floatPtr(price), PeakSince: timePtr(now)}}
	}

	var observed Patch
	since := r.State.PeakSince
	if since == nil {
		// Tracked before peak_since existed: tracking officially starts
		// now, and no high can be trusted yet.
		observed.PeakSince = timePtr(now)
	}

	peak := *r.State.PeakPriceUSD
	candidate := price
	if since != nil && highCounts(m, *since) && *m.HighUSD > candidate {
		candidate = *m.HighUSD
	}
	if candidate > peak {
		peak = candidate
		observed.PeakPriceUSD = floatPtr(peak)
	}
	if peak <= 0 || (peak-price)/peak*100 < r.Pct-pctEpsilon {
		return Outcome{Observed: observed}
	}

	trigger := triggerFor(r)
	trigger.PeakPriceUSD = peak
	return Outcome{
		Fire:     true,
		Trigger:  trigger,
		Observed: observed,
		OnSent: Patch{
			Fired: &FiredMark{At: now, PriceUSD: price},
			Alert: &AlertMark{At: now, PriceUSD: price},
		},
	}
}

// highCounts reports whether the source high may raise a peak tracked
// since `since`: its window must not start before tracking did.
func highCounts(m Market, since time.Time) bool {
	return m.HighUSD != nil && *m.HighUSD > 0 && m.HighSince != nil && !m.HighSince.Before(since)
}

// evalWeek52 compares the price with the 52-week extreme recorded at the
// previous evaluation. Twelve Data's fifty_two_week.high already includes
// today, so "price > current high" would almost never be true. The ref is
// replaced every evaluation (never accumulated), so an old extreme that
// leaves the 52-week window stops blocking new alerts (spec §5.2).
func evalWeek52(r Rule, m Market, now time.Time, opt Options) Outcome {
	var extreme *float64
	pick := math.Max
	switch r.Direction {
	case DirectionAbove:
		extreme = m.Week52High
	case DirectionBelow:
		extreme = m.Week52Low
		pick = math.Min
	default:
		return Outcome{Skip: SkipUnknownType}
	}
	if extreme == nil {
		return Outcome{Skip: SkipDataUnavailable}
	}

	price := m.PriceUSD
	// price is included because crypto 52-week values are daily prices
	// cached for 24 h and can sit below the current price.
	next := pick(*extreme, price)
	if r.State.BaselinePending {
		return Outcome{Skip: SkipBaseline, Observed: Patch{ClearBaseline: true, Week52RefUSD: floatPtr(next)}}
	}
	if r.State.Week52RefUSD == nil {
		return Outcome{Observed: Patch{Week52RefUSD: floatPtr(next)}}
	}

	ref := *r.State.Week52RefUSD
	broke := price > ref
	if r.Direction == DirectionBelow {
		broke = price < ref
	}
	if !broke {
		return Outcome{Observed: refPatch(ref, next)}
	}
	if inCooldown(r.State, price, now, opt) {
		return Outcome{Skip: SkipCooldown, Observed: refPatch(ref, next)}
	}

	trigger := triggerFor(r)
	trigger.PrevRefUSD = ref
	return Outcome{
		Fire:    true,
		Trigger: trigger,
		OnSent:  Patch{Week52RefUSD: floatPtr(next), Alert: alertMark(now, price, opt)},
	}
}

// refPatch avoids a database write when the 52-week ref did not change.
func refPatch(old, next float64) Patch {
	if old == next {
		return Patch{}
	}
	return Patch{Week52RefUSD: floatPtr(next)}
}
