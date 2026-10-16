package scheduler

import (
	"context"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/rules"
)

var ruleColumns = []string{
	"id", "user_id", "rule_type", "direction", "intent", "level_usd", "pct", "params_version",
	"baseline_pending", "last_beyond", "peak_price_usd", "peak_since", "week52_ref_usd",
	"last_alerted_price_usd", "cooldown_until",
	"telegram_asset_chat_id", "alert_cooldown_hours", "preferred_language", "level_currency", "level_idr",
}

func TestLoadRules_ScopedToUserAndMapped(t *testing.T) {
	w, mock, _ := newMockAssetWorker(t, 3)
	cooldown := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`FROM asset_rules r[\s\S]*AND r\.user_id = \?`).
		WithArgs("BTC", 7).
		WillReturnRows(sqlmock.NewRows(ruleColumns).
			AddRow(int64(11), int64(7), "price_level", "above", "take_profit", 120000.0, nil, int64(3),
				false, true, 118000.0, time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC), nil, 119000.0, cooldown, int64(7000), int64(4), "en", "IDR", 2150000000.0))

	rows, err := w.loadRules(context.Background(), "BTC", 7)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	r := rows[0]
	assert.Equal(t, uint64(11), r.Rule.ID)
	assert.Equal(t, rules.TypePriceLevel, r.Rule.Type)
	assert.Equal(t, rules.DirectionAbove, r.Rule.Direction)
	assert.Equal(t, rules.IntentTakeProfit, r.Rule.Intent)
	assert.Equal(t, 120000.0, r.Rule.LevelUSD)
	assert.Equal(t, rules.CurrencyIDR, r.Rule.Currency)
	assert.Equal(t, 2150000000.0, r.Rule.LevelIDR)
	assert.Equal(t, uint32(3), r.ParamsVersion)
	require.NotNil(t, r.Rule.State.LastBeyond)
	assert.True(t, *r.Rule.State.LastBeyond)
	require.NotNil(t, r.Rule.State.CooldownUntil)
	assert.Equal(t, cooldown, *r.Rule.State.CooldownUntil)
	assert.Equal(t, int64(7000), r.TelegramAssetChatID)
	require.NotNil(t, r.Rule.State.PeakSince)
	assert.Equal(t, time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC), *r.Rule.State.PeakSince)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBuildPatchUpdate(t *testing.T) {
	at := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	until := at.Add(4 * time.Hour)
	yes, peak := true, 101.5

	clauses, args := buildPatchUpdate(rules.Patch{ClearBaseline: true, LastBeyond: &yes, PeakPriceUSD: &peak})
	assert.Equal(t, []string{"baseline_pending = FALSE", "last_beyond = ?", "peak_price_usd = ?"}, clauses)
	assert.Equal(t, []interface{}{true, 101.5}, args)

	clauses, args = buildPatchUpdate(rules.Patch{Alert: &rules.AlertMark{At: at, PriceUSD: 90, CooldownUntil: &until}})
	assert.Equal(t, []string{"last_alerted_at = ?", "last_alerted_price_usd = ?", "cooldown_until = ?"}, clauses)
	assert.Equal(t, []interface{}{at, 90.0, until}, args)

	clauses, args = buildPatchUpdate(rules.Patch{
		Alert: &rules.AlertMark{At: at, PriceUSD: 90},
		Fired: &rules.FiredMark{At: at, PriceUSD: 90},
	})
	assert.Equal(t, []string{
		"last_alerted_at = ?", "last_alerted_price_usd = ?", "cooldown_until = ?",
		"is_active = FALSE", "triggered_at = ?", "triggered_price_usd = ?",
	}, clauses)
	assert.Equal(t, []interface{}{at, 90.0, nil, at, 90.0}, args, "no cooldown is written as NULL")

	since := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	clauses, args = buildPatchUpdate(rules.Patch{PeakPriceUSD: &peak, PeakSince: &since})
	assert.Equal(t, []string{"peak_price_usd = ?", "peak_since = ?"}, clauses)
	assert.Equal(t, []interface{}{101.5, since}, args)
}

func TestApplyPatch_GuardsOnParamsVersion(t *testing.T) {
	w, mock, _ := newMockAssetWorker(t, 3)
	no := false
	mock.ExpectExec(`UPDATE asset_rules SET last_beyond = \? WHERE id = \? AND params_version = \?`).
		WithArgs(false, 11, 3).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, w.applyPatch(context.Background(), 11, 3, rules.Patch{LastBeyond: &no}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyPatch_ZeroRowsIsNotAnError(t *testing.T) {
	w, mock, _ := newMockAssetWorker(t, 3)
	no := false
	mock.ExpectExec(`UPDATE asset_rules`).WillReturnResult(sqlmock.NewResult(0, 0))

	assert.NoError(t, w.applyPatch(context.Background(), 11, 3, rules.Patch{LastBeyond: &no}),
		"a rule edited mid-run must be skipped silently")
}

func TestApplyPatch_EmptyPatchWritesNothing(t *testing.T) {
	w, mock, _ := newMockAssetWorker(t, 3)
	require.NoError(t, w.applyPatch(context.Background(), 11, 3, rules.Patch{}))
	assert.NoError(t, mock.ExpectationsWereMet())
}
