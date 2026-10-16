package rules

import "math"

// NearPct: a level within this many percent of the price is "near". Mirrors
// NEAR_PCT in frontend/src/utils/ruleDistance.js.
const NearPct = 3

// DistanceKind says which fields of a Distance are set.
type DistanceKind string

const (
	DistanceNone        DistanceKind = "none"         // no distance (e.g. a 52-week rule not evaluated yet)
	DistanceLevel       DistanceKind = "level"        // Level, Pct, Passed, Near (+ PeakUSD for trailing)
	DistanceChange24h   DistanceKind = "change24h"    // Current, Threshold
	DistanceUnavailable DistanceKind = "unavailable"  // the price in the rule's currency is unknown
	DistanceWaitingPeak DistanceKind = "waiting_peak" // trailing stop without a peak yet
)

// Distance is how far the current price is from a rule, for the status
// report (#7). It is a fact about the price, never advice.
type Distance struct {
	Kind      DistanceKind
	Level     float64 // price_level: in the rule's currency; trailing_stop: the stop (rupiah for gold)
	PeakUSD   float64 // trailing_stop only
	PeakIDR   float64 // trailing_stop on gold (#58)
	Pct       float64 // (level - price) / price * 100
	Passed    bool
	Near      bool
	Current   *float64
	Threshold float64
}

// DistanceTo measures r against m without evaluating it. It mirrors
// ruleDistance in frontend/src/utils/ruleDistance.js; keep the two in sync.
func DistanceTo(r Rule, m Market) Distance {
	switch r.Type {
	case TypePriceLevel:
		if r.Direction != DirectionAbove && r.Direction != DirectionBelow {
			return Distance{Kind: DistanceNone}
		}
		price, level, ok := levelPair(r, m)
		if !ok || !(price > 0) || !(level > 0) {
			return Distance{Kind: DistanceUnavailable}
		}
		return levelDistance(level, r.Direction, price)
	case TypeTrailingStop:
		if r.Currency == CurrencyIDR {
			// Gold is tracked in rupiah (#58).
			if r.State.PeakPriceIDR == nil {
				return Distance{Kind: DistanceWaitingPeak}
			}
			if m.PriceIDR == nil || !(*m.PriceIDR > 0) {
				return Distance{Kind: DistanceUnavailable}
			}
			peak := *r.State.PeakPriceIDR
			d := levelDistance(peak*(1-r.Pct/100), DirectionBelow, *m.PriceIDR)
			d.PeakIDR = peak
			return d
		}
		if r.State.PeakPriceUSD == nil {
			return Distance{Kind: DistanceWaitingPeak}
		}
		if !(m.PriceUSD > 0) {
			return Distance{Kind: DistanceUnavailable}
		}
		peak := *r.State.PeakPriceUSD
		d := levelDistance(peak*(1-r.Pct/100), DirectionBelow, m.PriceUSD)
		d.PeakUSD = peak
		return d
	case TypePctChange24h:
		return Distance{Kind: DistanceChange24h, Current: m.ChangePct24h, Threshold: r.Pct}
	case TypeWeek52Break:
		// The engine compares the price with the extreme stored at the
		// previous evaluation (#52); before that there is nothing to measure.
		ref := r.State.Week52RefUSD
		if ref == nil || !(*ref > 0) || !(m.PriceUSD > 0) ||
			(r.Direction != DirectionAbove && r.Direction != DirectionBelow) {
			return Distance{Kind: DistanceNone}
		}
		return levelDistance(*ref, r.Direction, m.PriceUSD)
	}
	return Distance{Kind: DistanceNone}
}

func levelDistance(level float64, side Direction, price float64) Distance {
	pct := (level - price) / price * 100
	passed := isBeyond(side, price, level)
	return Distance{Kind: DistanceLevel, Level: level, Pct: pct, Passed: passed, Near: !passed && math.Abs(pct) <= NearPct}
}

// TriggerOf is r's definition in the shape the message builders take, for a
// report line about a rule that did not fire.
func TriggerOf(r Rule) Trigger {
	return triggerFor(r)
}
