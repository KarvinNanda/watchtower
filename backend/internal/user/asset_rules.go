package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/karvin-nanda/watchtower/internal/asset"
)

// Errors returned by the asset rule operations.
var (
	ErrInvalidRuleType         = errors.New("rule_type must be one of: price_level, pct_change_24h, trailing_stop, week52_break")
	ErrInvalidDirection        = errors.New("direction must be one of: above, below")
	ErrInvalidIntent           = errors.New("intent must be one of: buy_target, take_profit, stop_loss")
	ErrIntentDirectionConflict = errors.New("direction conflicts with intent")
	ErrMissingLevel            = errors.New("price_level needs exactly one of level_usd (0.00000001 to 999999999) or level_idr (0.0001 to 999999999999)")
	ErrInvalidPct              = errors.New("pct must be at least 0.01 and at most 100 (below 100 for trailing_stop)")
	ErrFieldNotAllowed         = errors.New("a field was set that does not apply to this rule_type")
	ErrWeek52NotForGold        = errors.New("week52_break is not available for gold")
	ErrDuplicateRule           = errors.New("an identical active rule already exists")
	ErrCannotRearm             = errors.New("only a trailing_stop that has fired can be re-armed")
	ErrRuleNotFound            = errors.New("rule not found")
	ErrSymbolTypeMismatch      = errors.New("asset_symbol does not belong to asset_type")
	ErrEmptyUpdate             = errors.New("nothing to update: send a rule definition, is_active, or both")
)

// symbolTypeError names the symbol and type in its message ("BTC is not a
// stock symbol") and matches ErrSymbolTypeMismatch with errors.Is.
type symbolTypeError struct{ symbol, assetType string }

func (e symbolTypeError) Error() string        { return e.symbol + " is not a " + e.assetType + " symbol" }
func (e symbolTypeError) Is(target error) bool { return target == ErrSymbolTypeMismatch }

const (
	ruleTypePriceLevel   = "price_level"
	ruleTypePctChange24h = "pct_change_24h"
	ruleTypeTrailingStop = "trailing_stop"
	ruleTypeWeek52Break  = "week52_break"
	maxRulePct           = 100
	// The smallest values the columns can store (pct DECIMAL(5,2),
	// level_usd DECIMAL(20,8)). Anything smaller would be saved as 0: a
	// 0% trailing stop fires on the next run, a 0% change rule on any move.
	minRulePct  = 0.01
	minLevelUSD = 1e-8
	// level_idr DECIMAL(24,8): BTC is about Rp 1.5 billion, far above the
	// USD cap. Below 0.0001 a rupiah level means nothing.
	minLevelIDR      = 1e-4
	maxLevelIDR      = 999999999999
	levelCurrencyUSD = "USD"
	levelCurrencyIDR = "IDR"
)

var validRuleTypes = map[string]bool{
	ruleTypePriceLevel: true, ruleTypePctChange24h: true, ruleTypeTrailingStop: true, ruleTypeWeek52Break: true,
}
var validDirections = map[string]bool{"above": true, "below": true}

// intentDirections mirrors rules.IntentDirection (spec Q2).
var intentDirections = map[string]string{"buy_target": "below", "take_profit": "above", "stop_loss": "below"}

// stateResetKeepFiredSQL clears every scheduler-owned column except the
// one-shot "fired" record of a trailing stop.
const stateResetKeepFiredSQL = "baseline_pending = FALSE, last_beyond = NULL, peak_price_usd = NULL, peak_since = NULL, " +
	"week52_ref_usd = NULL, last_alerted_at = NULL, last_alerted_price_usd = NULL, cooldown_until = NULL, " +
	"peak_price_idr = NULL" // #58: the rupiah peak too

// stateResetSQL clears every scheduler-owned column, so an edited or
// re-armed rule starts like a new one.
const stateResetSQL = stateResetKeepFiredSQL + ", triggered_at = NULL, triggered_price_usd = NULL, triggered_price_idr = NULL"

// AssetRule mirrors an asset_rules row, including the read-only state the
// UI shows.
type AssetRule struct {
	ID            uint64
	AssetType     string
	AssetSymbol   string
	RuleType      string
	Direction     *string
	Intent        *string
	LevelUSD      *float64
	LevelCurrency string // "USD" or "IDR"; price_level rules only differ
	LevelIDR      *float64
	Pct           *float64
	IsActive      bool
	PeakPriceUSD  *float64
	// Week52RefUSD is the 52-week extreme stored at the rule's previous
	// evaluation: the level a week52_break rule compares the price with (#52).
	Week52RefUSD      *float64
	TriggeredAt       *time.Time
	TriggeredPriceUSD *float64
	// #58: gold trailing stops, in rupiah.
	PeakPriceIDR      *float64
	TriggeredPriceIDR *float64
	LastAlertedAt     *time.Time
	CreatedAt         time.Time
	// Supported is false for a symbol WatchTower cannot fetch (not on the
	// whitelist, or listed under another asset type), e.g. a legacy
	// free-form symbol copied from asset_subscriptions. Such a rule never fires.
	Supported bool
}

// AssetRuleInput is a rule definition from the API. It never carries state.
type AssetRuleInput struct {
	AssetType   string
	AssetSymbol string
	RuleType    string
	Direction   *string
	Intent      *string
	LevelUSD    *float64
	LevelIDR    *float64
	// LevelCurrency is derived by normalizeAssetRule from the level field
	// that is set; any value passed in is ignored.
	LevelCurrency string
	Pct           *float64
}

// UpdateAssetRuleRequest replaces the definition (Rule != nil), toggles
// is_active (IsActive != nil), or both.
type UpdateAssetRuleRequest struct {
	Rule     *AssetRuleInput
	IsActive *bool
}

// queryer is satisfied by both *sql.DB and *sql.Tx.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// normalizeAssetRule validates in against the per-type field matrix in
// spec §7.1 and returns the cleaned definition. A field that does not
// apply to the rule type is rejected, never silently dropped.
func normalizeAssetRule(in AssetRuleInput) (AssetRuleInput, error) {
	out := AssetRuleInput{AssetType: in.AssetType, AssetSymbol: sanitizeAssetSymbol(in.AssetSymbol), RuleType: in.RuleType, LevelCurrency: levelCurrencyUSD}

	if !validAssetTypes[out.AssetType] {
		return AssetRuleInput{}, ErrInvalidAssetType
	}
	if out.AssetSymbol == "" || !assetSymbolPattern.MatchString(out.AssetSymbol) {
		return AssetRuleInput{}, ErrInvalidAssetSymbol
	}
	t, ok := asset.SymbolAssetType(out.AssetSymbol)
	if !ok {
		return AssetRuleInput{}, ErrInvalidAssetSymbol
	}
	if t != out.AssetType {
		return AssetRuleInput{}, symbolTypeError{symbol: out.AssetSymbol, assetType: out.AssetType}
	}
	if !validRuleTypes[out.RuleType] {
		return AssetRuleInput{}, ErrInvalidRuleType
	}

	switch out.RuleType {
	case ruleTypePriceLevel:
		if in.Pct != nil {
			return AssetRuleInput{}, ErrFieldNotAllowed
		}
		switch {
		case in.LevelUSD != nil && in.LevelIDR == nil:
			if !isValidPrice(*in.LevelUSD) || *in.LevelUSD < minLevelUSD {
				return AssetRuleInput{}, ErrMissingLevel
			}
			out.LevelUSD = in.LevelUSD
		case in.LevelIDR != nil && in.LevelUSD == nil:
			// NaN fails both comparisons, +Inf fails the upper bound.
			if !(*in.LevelIDR >= minLevelIDR && *in.LevelIDR <= maxLevelIDR) {
				return AssetRuleInput{}, ErrMissingLevel
			}
			out.LevelIDR, out.LevelCurrency = in.LevelIDR, levelCurrencyIDR
		default:
			return AssetRuleInput{}, ErrMissingLevel
		}
		dir, err := resolveDirection(in.Direction, in.Intent)
		if err != nil {
			return AssetRuleInput{}, err
		}
		out.Direction, out.Intent = &dir, in.Intent

	case ruleTypePctChange24h, ruleTypeTrailingStop:
		if in.Direction != nil || in.Intent != nil || in.LevelUSD != nil || in.LevelIDR != nil {
			return AssetRuleInput{}, ErrFieldNotAllowed
		}
		ok := in.Pct != nil && *in.Pct >= minRulePct && *in.Pct <= maxRulePct
		if ok && out.RuleType == ruleTypeTrailingStop {
			ok = *in.Pct < maxRulePct
		}
		if !ok {
			return AssetRuleInput{}, ErrInvalidPct
		}
		out.Pct = in.Pct

	case ruleTypeWeek52Break:
		if in.Intent != nil || in.LevelUSD != nil || in.LevelIDR != nil || in.Pct != nil {
			return AssetRuleInput{}, ErrFieldNotAllowed
		}
		if out.AssetType == "gold" {
			return AssetRuleInput{}, ErrWeek52NotForGold
		}
		if in.Direction == nil || !validDirections[*in.Direction] {
			return AssetRuleInput{}, ErrInvalidDirection
		}
		out.Direction = in.Direction
	}
	return out, nil
}

// resolveDirection derives the direction from the intent when there is
// one, and rejects a conflicting explicit direction instead of fixing it.
func resolveDirection(direction, intent *string) (string, error) {
	if intent != nil {
		implied, ok := intentDirections[*intent]
		if !ok {
			return "", ErrInvalidIntent
		}
		if direction != nil && *direction != implied {
			return "", ErrIntentDirectionConflict
		}
		return implied, nil
	}
	if direction == nil || !validDirections[*direction] {
		return "", ErrInvalidDirection
	}
	return *direction, nil
}

// GetAssetRules returns every rule owned by userID, grouped by symbol.
func (s *UserService) GetAssetRules(userID uint64) ([]AssetRule, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, asset_type, asset_symbol, rule_type, direction, intent, level_usd, pct, is_active,
			peak_price_usd, triggered_at, triggered_price_usd, last_alerted_at, created_at, level_currency, level_idr, week52_ref_usd,
			peak_price_idr, triggered_price_idr
		FROM asset_rules
		WHERE user_id = ?
		ORDER BY asset_symbol, created_at`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("user: query asset_rules: %w", err)
	}
	defer rows.Close()

	out := make([]AssetRule, 0)
	for rows.Next() {
		var (
			r                           AssetRule
			direction, intent           sql.NullString
			level, pct, peak, trigPrice sql.NullFloat64
			levelIDR, week52Ref         sql.NullFloat64
			peakIDR, trigIDR            sql.NullFloat64
			triggeredAt, lastAlertedAt  sql.NullTime
		)
		if err := rows.Scan(&r.ID, &r.AssetType, &r.AssetSymbol, &r.RuleType, &direction, &intent, &level, &pct,
			&r.IsActive, &peak, &triggeredAt, &trigPrice, &lastAlertedAt, &r.CreatedAt,
			&r.LevelCurrency, &levelIDR, &week52Ref, &peakIDR, &trigIDR); err != nil {
			return nil, fmt.Errorf("user: scan asset_rules: %w", err)
		}
		r.Direction, r.Intent = nullStringPtr(direction), nullStringPtr(intent)
		r.LevelUSD, r.Pct, r.PeakPriceUSD, r.TriggeredPriceUSD = nullFloatPtr(level), nullFloatPtr(pct), nullFloatPtr(peak), nullFloatPtr(trigPrice)
		r.LevelIDR, r.Week52RefUSD = nullFloatPtr(levelIDR), nullFloatPtr(week52Ref)
		r.PeakPriceIDR, r.TriggeredPriceIDR = nullFloatPtr(peakIDR), nullFloatPtr(trigIDR)
		r.TriggeredAt, r.LastAlertedAt = nullTimePtr(triggeredAt), nullTimePtr(lastAlertedAt)
		t, ok := asset.SymbolAssetType(r.AssetSymbol)
		r.Supported = ok && t == r.AssetType
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateAssetRule validates and inserts a new rule for userID and returns
// its ID. The duplicate check and the insert are not atomic; two identical
// concurrent requests from the same user can both succeed, which only
// costs a duplicate alert line.
func (s *UserService) CreateAssetRule(userID uint64, in AssetRuleInput) (uint64, error) {
	r, err := normalizeAssetRule(in)
	if err != nil {
		return 0, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	if err := checkDuplicateRule(ctx, s.db, userID, 0, r); err != nil {
		return 0, err
	}
	if err := s.checkRuleSymbolLimit(ctx, s.db, r.AssetSymbol); err != nil {
		return 0, err
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO asset_rules (user_id, asset_type, asset_symbol, rule_type, direction, intent, level_usd, pct, level_currency, level_idr)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, r.AssetType, r.AssetSymbol, r.RuleType, r.Direction, r.Intent, r.LevelUSD, r.Pct, r.LevelCurrency, r.LevelIDR,
	)
	if err != nil {
		return 0, fmt.Errorf("user: insert asset_rules: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("user: read new asset rule id: %w", err)
	}
	if id < 0 {
		return 0, fmt.Errorf("user: read new asset rule id: negative id %d", id)
	}
	return uint64(id), nil
}

// UpdateAssetRule applies req to ruleID, which must belong to userID.
func (s *UserService) UpdateAssetRule(userID, ruleID uint64, req UpdateAssetRuleRequest) error {
	if req.Rule == nil && req.IsActive == nil {
		return ErrEmptyUpdate
	}
	var def *AssetRuleInput
	if req.Rule != nil {
		n, err := normalizeAssetRule(*req.Rule)
		if err != nil {
			return err
		}
		def = &n
	}
	return s.updateRule(userID, ruleID, def, req.IsActive, false)
}

// RearmAssetRule reactivates a fired trailing stop with a fresh baseline.
func (s *UserService) RearmAssetRule(userID, ruleID uint64) error {
	active := true
	return s.updateRule(userID, ruleID, nil, &active, true)
}

type currentRule struct {
	def      AssetRuleInput
	isActive bool
	fired    bool
}

// updateRule is the single write path for edits, toggles and re-arm. Every
// path bumps params_version so the scheduler's in-flight state writes for
// the old version are discarded (spec §6.3).
func (s *UserService) updateRule(userID, ruleID uint64, def *AssetRuleInput, isActive *bool, rearm bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("user: begin asset rule update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	cur, err := loadRuleForUpdate(ctx, tx, userID, ruleID)
	if err != nil {
		return err
	}
	firedTrailing := cur.def.RuleType == ruleTypeTrailingStop && cur.fired
	if rearm && !firedTrailing {
		return ErrCannotRearm
	}

	// Saving an edit form without changes must not reset state, or a
	// crossed level alerts again and a trailing stop loses its peak.
	if def != nil && sameDefinition(*def, cur.def) {
		def = nil
	}

	target := cur.def
	if def != nil {
		target = *def
	}
	active := cur.isActive
	if isActive != nil {
		active = *isActive
	}

	if active {
		if err := checkDuplicateRule(ctx, tx, userID, ruleID, target); err != nil {
			return err
		}
		if !cur.isActive || target.AssetSymbol != cur.def.AssetSymbol {
			if err := s.checkRuleSymbolLimit(ctx, tx, target.AssetSymbol); err != nil {
				return err
			}
		}
	}

	clauses := []string{"is_active = ?", "params_version = params_version + 1"}
	args := []interface{}{active}
	if def != nil {
		clauses = append(clauses, "asset_type = ?", "asset_symbol = ?", "rule_type = ?", "direction = ?", "intent = ?", "level_usd = ?", "pct = ?",
			"level_currency = ?", "level_idr = ?")
		args = append(args, def.AssetType, def.AssetSymbol, def.RuleType, def.Direction, def.Intent, def.LevelUSD, def.Pct,
			def.LevelCurrency, def.LevelIDR)
	}
	// Turning a fired trailing stop back on (re-arm or is_active=true) must
	// not reuse its old peak, or it fires again on the next run (spec §7.1).
	reactivatingFired := firedTrailing && active && !cur.isActive
	// Editing a fired trailing stop that stays off resets its peak but keeps
	// the fired record, so the UI still shows the badge and Re-arm (#26).
	keepFired := firedTrailing && !active && target.RuleType == ruleTypeTrailingStop
	switch {
	case rearm || reactivatingFired:
		clauses = append(clauses, stateResetSQL)
	case def != nil && keepFired:
		clauses = append(clauses, stateResetKeepFiredSQL)
	case def != nil:
		clauses = append(clauses, stateResetSQL)
	}
	args = append(args, ruleID, userID)

	// #nosec G201 -- clauses only ever contains hardcoded literals chosen
	// above; all user-supplied values flow through args as bind parameters.
	query := fmt.Sprintf("UPDATE asset_rules SET %s WHERE id = ? AND user_id = ?", strings.Join(clauses, ", "))
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("user: update asset_rules: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("user: commit asset rule update: %w", err)
	}
	return nil
}

// sameDefinition compares two rule definitions at the precision MySQL
// stores them (level_usd DECIMAL(20,8), level_idr DECIMAL(24,8), pct
// DECIMAL(5,2)). A currency change is a new definition even when the number
// is the same.
func sameDefinition(a, b AssetRuleInput) bool {
	return a.AssetType == b.AssetType && a.AssetSymbol == b.AssetSymbol && a.RuleType == b.RuleType &&
		sameString(a.Direction, b.Direction) && sameString(a.Intent, b.Intent) &&
		sameRounded(a.LevelUSD, b.LevelUSD, 1e8) && sameRounded(a.Pct, b.Pct, 1e2) &&
		a.LevelCurrency == b.LevelCurrency && sameRounded(a.LevelIDR, b.LevelIDR, 1e8)
}

func sameString(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameRounded(a, b *float64, scale float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return math.Round(*a*scale) == math.Round(*b*scale)
}

// loadRuleForUpdate locks ruleID's row. Not found and not owned by userID
// both return ErrRuleNotFound, so the API never reveals other users' rules.
func loadRuleForUpdate(ctx context.Context, tx *sql.Tx, userID, ruleID uint64) (currentRule, error) {
	var (
		cur               currentRule
		direction, intent sql.NullString
		level, pct        sql.NullFloat64
		levelIDR          sql.NullFloat64
	)
	err := tx.QueryRowContext(ctx, `
		SELECT asset_type, asset_symbol, rule_type, direction, intent, level_usd, pct, is_active, triggered_at IS NOT NULL,
			level_currency, level_idr
		FROM asset_rules
		WHERE id = ? AND user_id = ?
		FOR UPDATE`, ruleID, userID,
	).Scan(&cur.def.AssetType, &cur.def.AssetSymbol, &cur.def.RuleType, &direction, &intent, &level, &pct, &cur.isActive, &cur.fired,
		&cur.def.LevelCurrency, &levelIDR)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return currentRule{}, ErrRuleNotFound
		}
		return currentRule{}, fmt.Errorf("user: load asset rule: %w", err)
	}
	cur.def.Direction, cur.def.Intent = nullStringPtr(direction), nullStringPtr(intent)
	cur.def.LevelUSD, cur.def.Pct = nullFloatPtr(level), nullFloatPtr(pct)
	cur.def.LevelIDR = nullFloatPtr(levelIDR)
	return cur, nil
}

// DeleteAssetRule removes ruleID, which must belong to userID. This is a
// hard delete (spec §14.3); alert history stays in notification_logs.
func (s *UserService) DeleteAssetRule(userID, ruleID uint64) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	res, err := s.db.ExecContext(ctx, `DELETE FROM asset_rules WHERE id = ? AND user_id = ?`, ruleID, userID)
	if err != nil {
		return fmt.Errorf("user: delete asset_rules: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("user: delete asset_rules rows: %w", err)
	}
	if n == 0 {
		return ErrRuleNotFound
	}
	return nil
}

// checkDuplicateRule rejects a rule identical to another active rule of
// the same user. <=> is MySQL's NULL-safe equality, because NULL = NULL is
// not true in SQL.
func checkDuplicateRule(ctx context.Context, q queryer, userID, excludeID uint64, r AssetRuleInput) error {
	var exists bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM asset_rules
			WHERE user_id = ? AND asset_symbol = ? AND rule_type = ?
				AND direction <=> ? AND intent <=> ? AND level_usd <=> ? AND pct <=> ?
				AND level_currency = ? AND level_idr <=> ?
				AND is_active = TRUE AND id <> ?
		)`,
		userID, r.AssetSymbol, r.RuleType, r.Direction, r.Intent, r.LevelUSD, r.Pct, r.LevelCurrency, r.LevelIDR, excludeID,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("user: check duplicate rule: %w", err)
	}
	if exists {
		return ErrDuplicateRule
	}
	return nil
}

// checkRuleSymbolLimit enforces MAX_UNIQUE_SYMBOLS system-wide over active
// rules. A symbol already tracked never counts against the limit again.
func (s *UserService) checkRuleSymbolLimit(ctx context.Context, q queryer, assetSymbol string) error {
	var alreadyTracked bool
	if err := q.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM asset_rules WHERE asset_symbol = ? AND is_active = true)`, assetSymbol,
	).Scan(&alreadyTracked); err != nil {
		return fmt.Errorf("user: check existing symbol: %w", err)
	}
	if alreadyTracked {
		return nil
	}

	var distinctCount int
	if err := q.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT asset_symbol) FROM asset_rules WHERE is_active = true`,
	).Scan(&distinctCount); err != nil {
		return fmt.Errorf("user: count unique symbols: %w", err)
	}
	if distinctCount >= s.maxUniqueSymbols {
		return ErrMaxUniqueSymbolsReached
	}
	return nil
}

func nullStringPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func nullFloatPtr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

func nullTimePtr(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	v := n.Time
	return &v
}
