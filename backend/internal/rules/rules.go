// Package rules evaluates one asset alert rule against one market snapshot.
// It is pure: no database, no HTTP, no clock. The scheduler loads rules,
// calls Evaluate, and persists the returned patches.
package rules

import "time"

// Type is an asset rule's kind, matching asset_rules.rule_type.
type Type string

const (
	TypePriceLevel   Type = "price_level"
	TypePctChange24h Type = "pct_change_24h"
	TypeTrailingStop Type = "trailing_stop"
	TypeWeek52Break  Type = "week52_break"
)

// Direction says which side of a level, or which 52-week extreme, a rule
// watches.
type Direction string

const (
	DirectionNone  Direction = ""
	DirectionAbove Direction = "above"
	DirectionBelow Direction = "below"
)

// Intent is the user's own label for a price level. WatchTower never
// derives one; it only repeats the user's plan back in the alert.
type Intent string

const (
	IntentNone       Intent = ""
	IntentBuyTarget  Intent = "buy_target"
	IntentTakeProfit Intent = "take_profit"
	IntentStopLoss   Intent = "stop_loss"
)

// IntentDirection returns the direction an intent implies: a buy target
// and a stop loss are reached from above, a take profit from below.
func IntentDirection(i Intent) (Direction, bool) {
	switch i {
	case IntentBuyTarget, IntentStopLoss:
		return DirectionBelow, true
	case IntentTakeProfit:
		return DirectionAbove, true
	}
	return DirectionNone, false
}

// State is the scheduler-owned part of an asset_rules row.
type State struct {
	BaselinePending bool
	LastBeyond      *bool
	PeakPriceUSD    *float64
	PeakSince       *time.Time // when peak tracking started (creation, re-arm or migration)
	// PeakPriceIDR is a gold trailing stop's peak on the rupiah price (#58).
	PeakPriceIDR        *float64
	Week52RefUSD        *float64
	LastAlertedPriceUSD *float64
	CooldownUntil       *time.Time
}

// Currency is the currency a price_level rule's level is entered in
// (asset_rules.level_currency). The zero value means USD.
type Currency string

const (
	CurrencyUSD Currency = "USD"
	CurrencyIDR Currency = "IDR"
)

// Rule is one asset_rules row as the evaluator needs it.
type Rule struct {
	ID        uint64
	Type      Type
	Direction Direction
	Intent    Intent
	LevelUSD  float64
	Currency  Currency
	LevelIDR  float64 // price_level with Currency IDR
	Pct       float64
	State     State
}

// Market is the data a rule is evaluated against. A nil pointer means the
// value is unknown or not supported for this asset.
type Market struct {
	PriceUSD float64
	// PriceIDR is market_cache.price_idr from the same fetch (gold: the
	// source price; others: converted with a real daily rate). Nil when
	// unknown; never derived from the fallback rate (#41).
	PriceIDR     *float64
	ChangePct24h *float64
	Week52High   *float64
	Week52Low    *float64
	// HighUSD is the source's recent high and HighSince the start of the
	// window it covers. Either nil means unknown.
	HighUSD   *float64
	HighSince *time.Time
}

// Options carries the per-run and per-user settings Evaluate needs.
type Options struct {
	SkipCooldown      bool    // manual "Check now"
	AlertPriceMovePct float64 // ALERT_PRICE_MOVE_PCT
	CooldownHours     int     // users.alert_cooldown_hours
}

// SkipReason explains why a rule did not fire when that is not simply
// "the condition is false".
type SkipReason string

const (
	SkipNone            SkipReason = ""
	SkipDataUnavailable SkipReason = "data_unavailable"
	SkipBaseline        SkipReason = "baseline"
	SkipCooldown        SkipReason = "cooldown"
	SkipUnknownType     SkipReason = "unknown_rule_type"
)

// AlertMark records a delivered alert: last_alerted_at,
// last_alerted_price_usd and cooldown_until (NULL when CooldownUntil is nil).
type AlertMark struct {
	At            time.Time
	PriceUSD      float64
	CooldownUntil *time.Time
}

// FiredMark records a one-shot rule firing: is_active = FALSE,
// triggered_at and triggered_price_usd.
type FiredMark struct {
	At       time.Time
	PriceUSD float64
	PriceIDR float64 // rupiah price for a gold trailing stop; 0 otherwise
}

// Patch is a set of state changes for one rule. Nil or false fields are
// left unchanged.
type Patch struct {
	ClearBaseline bool
	LastBeyond    *bool
	PeakPriceUSD  *float64
	PeakSince     *time.Time
	PeakPriceIDR  *float64
	Week52RefUSD  *float64
	Alert         *AlertMark
	Fired         *FiredMark
}

// IsEmpty reports whether the patch changes nothing.
func (p Patch) IsEmpty() bool {
	return !p.ClearBaseline && p.LastBeyond == nil && p.PeakPriceUSD == nil && p.PeakSince == nil && p.PeakPriceIDR == nil &&
		p.Week52RefUSD == nil && p.Alert == nil && p.Fired == nil
}

// Trigger is what the alert message needs to describe why a rule fired.
type Trigger struct {
	Type         Type
	Direction    Direction
	Intent       Intent
	LevelUSD     float64
	Currency     Currency
	LevelIDR     float64
	Pct          float64
	PeakPriceUSD float64 // trailing_stop: the peak the drop is measured from
	PeakPriceIDR float64 // trailing_stop on gold: the rupiah peak (#58)
	PrevRefUSD   float64 // week52_break: the 52-week extreme that was broken
}

// Outcome is the result of evaluating one rule. Observed must be persisted
// after every evaluation; OnSent only after the alert was delivered, so a
// failed send is retried on the next run instead of being lost.
type Outcome struct {
	Fire     bool
	Trigger  Trigger
	Skip     SkipReason
	Observed Patch
	OnSent   Patch
}
