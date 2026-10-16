package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"

	"github.com/karvin-nanda/watchtower/internal/rules"
)

// ruleRow is one active asset rule joined with its owner's notification
// settings.
type ruleRow struct {
	Rule                rules.Rule
	UserID              uint64
	ParamsVersion       uint32
	TelegramAssetChatID int64
	AlertCooldownHours  int
	PreferredLanguage   string
}

// loadRules returns every active rule on symbol whose owner is active and
// has a Telegram asset chat linked (only userID's rules when userID != 0).
func (w *AssetWorker) loadRules(ctx context.Context, symbol string, userID uint64) ([]ruleRow, error) {
	query := `
		SELECT r.id, r.user_id, r.rule_type, r.direction, r.intent, r.level_usd, r.pct, r.params_version,
			r.baseline_pending, r.last_beyond, r.peak_price_usd, r.peak_since, r.week52_ref_usd,
			r.last_alerted_price_usd, r.cooldown_until,
			u.telegram_asset_chat_id, u.alert_cooldown_hours, u.preferred_language,
			r.level_currency, r.level_idr
		FROM asset_rules r
		JOIN users u ON u.id = r.user_id
		WHERE r.asset_symbol = ?
			AND r.is_active = true
			AND u.is_active = true
			AND u.telegram_asset_chat_id IS NOT NULL`
	args := []interface{}{symbol}
	if userID != 0 {
		query += ` AND r.user_id = ?`
		args = append(args, userID)
	}
	query += ` ORDER BY r.id`

	rows, err := w.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query rules for %s: %w", symbol, err)
	}
	defer rows.Close()

	var out []ruleRow
	for rows.Next() {
		var (
			row                         ruleRow
			ruleType                    string
			direction, intent           sql.NullString
			level, pct                  sql.NullFloat64
			lastBeyond                  sql.NullBool
			peak, ref, lastAlertedPrice sql.NullFloat64
			cooldownUntil, peakSince    sql.NullTime
			levelCurrency               string
			levelIDR                    sql.NullFloat64
		)
		if err := rows.Scan(
			&row.Rule.ID, &row.UserID, &ruleType, &direction, &intent, &level, &pct, &row.ParamsVersion,
			&row.Rule.State.BaselinePending, &lastBeyond, &peak, &peakSince, &ref,
			&lastAlertedPrice, &cooldownUntil,
			&row.TelegramAssetChatID, &row.AlertCooldownHours, &row.PreferredLanguage,
			&levelCurrency, &levelIDR,
		); err != nil {
			return nil, fmt.Errorf("scan rule row for %s: %w", symbol, err)
		}

		row.Rule.Type = rules.Type(ruleType)
		row.Rule.Direction = rules.Direction(direction.String)
		row.Rule.Intent = rules.Intent(intent.String)
		row.Rule.LevelUSD = level.Float64
		row.Rule.Currency = rules.Currency(levelCurrency)
		row.Rule.LevelIDR = levelIDR.Float64
		row.Rule.Pct = pct.Float64
		if lastBeyond.Valid {
			row.Rule.State.LastBeyond = &lastBeyond.Bool
		}
		row.Rule.State.PeakPriceUSD = nullFloat(peak)
		if peakSince.Valid {
			row.Rule.State.PeakSince = &peakSince.Time
		}
		row.Rule.State.Week52RefUSD = nullFloat(ref)
		row.Rule.State.LastAlertedPriceUSD = nullFloat(lastAlertedPrice)
		if cooldownUntil.Valid {
			row.Rule.State.CooldownUntil = &cooldownUntil.Time
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func nullFloat(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

// buildPatchUpdate turns a patch into SET clauses and bind args. Clauses
// are fixed literals; values only ever travel as bind parameters.
func buildPatchUpdate(p rules.Patch) (clauses []string, args []interface{}) {
	if p.ClearBaseline {
		clauses = append(clauses, "baseline_pending = FALSE")
	}
	if p.LastBeyond != nil {
		clauses = append(clauses, "last_beyond = ?")
		args = append(args, *p.LastBeyond)
	}
	if p.PeakPriceUSD != nil {
		clauses = append(clauses, "peak_price_usd = ?")
		args = append(args, *p.PeakPriceUSD)
	}
	if p.PeakSince != nil {
		clauses = append(clauses, "peak_since = ?")
		args = append(args, *p.PeakSince)
	}
	if p.Week52RefUSD != nil {
		clauses = append(clauses, "week52_ref_usd = ?")
		args = append(args, *p.Week52RefUSD)
	}
	if p.Alert != nil {
		var cooldown interface{}
		if p.Alert.CooldownUntil != nil {
			cooldown = *p.Alert.CooldownUntil
		}
		clauses = append(clauses, "last_alerted_at = ?", "last_alerted_price_usd = ?", "cooldown_until = ?")
		args = append(args, p.Alert.At, p.Alert.PriceUSD, cooldown)
	}
	if p.Fired != nil {
		clauses = append(clauses, "is_active = FALSE", "triggered_at = ?", "triggered_price_usd = ?")
		args = append(args, p.Fired.At, p.Fired.PriceUSD)
	}
	return clauses, args
}

// applyPatch writes p to rule ruleID only if its params_version still
// matches the one read at load time. If the user edited the rule
// meanwhile, the write matches zero rows and is skipped: state computed
// against the old parameters must not land on the new ones (spec §6.3).
func (w *AssetWorker) applyPatch(ctx context.Context, ruleID uint64, paramsVersion uint32, p rules.Patch) error {
	clauses, args := buildPatchUpdate(p)
	if len(clauses) == 0 {
		return nil
	}
	args = append(args, ruleID, paramsVersion)

	// #nosec G201 -- clauses only ever contains the hardcoded "col = ?"
	// literals from buildPatchUpdate; all values are bind parameters.
	query := fmt.Sprintf("UPDATE asset_rules SET %s WHERE id = ? AND params_version = ?", strings.Join(clauses, ", "))
	res, err := w.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update asset_rules %d: %w", ruleID, err)
	}
	// MySQL also reports 0 when the values were already equal, so this is
	// only a debug hint, never an error.
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		log.Printf("[DEBUG] applyPatch: rule %d not updated (edited since load, or unchanged)", ruleID)
	}
	return nil
}
