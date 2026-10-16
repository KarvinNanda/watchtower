package user_test

import (
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/user"
)

// floatPtr, strPtr and boolPtr already exist in user_test.go (same package).

var lockColumns = []string{"asset_type", "asset_symbol", "rule_type", "direction", "intent", "level_usd", "pct", "is_active", "fired", "level_currency", "level_idr"}

func TestCreateAssetRule_InsertsNormalizedRule(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectQuery(`SELECT EXISTS\(\s*SELECT 1 FROM asset_rules\s*WHERE user_id = \?`).
		WithArgs(7, "BTC", "price_level", "above", "take_profit", 120000.0, nil, "USD", nil, 0).
		WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	mock.ExpectQuery(`asset_symbol = \? AND is_active = true\)`).WithArgs("BTC").
		WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(true))
	mock.ExpectExec(`INSERT INTO asset_rules`).
		WithArgs(7, "crypto", "BTC", "price_level", "above", "take_profit", 120000.0, nil, "USD", nil).
		WillReturnResult(sqlmock.NewResult(42, 1))

	id, err := svc.CreateAssetRule(7, user.AssetRuleInput{
		AssetType: "crypto", AssetSymbol: "btc", RuleType: "price_level", Intent: strPtr("take_profit"), LevelUSD: floatPtr(120000),
	})

	require.NoError(t, err)
	assert.Equal(t, uint64(42), id)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A negative insert id (driver bug or wrap) must be an error, never a huge
// uint64 rule id (gosec G115, code scanning alert #1).
func TestCreateAssetRule_NegativeInsertIDIsAnError(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectQuery(`SELECT EXISTS\(\s*SELECT 1 FROM asset_rules\s*WHERE user_id = \?`).
		WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	mock.ExpectQuery(`asset_symbol = \? AND is_active = true\)`).WithArgs("BTC").
		WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(true))
	mock.ExpectExec(`INSERT INTO asset_rules`).WillReturnResult(sqlmock.NewResult(-1, 1))

	id, err := svc.CreateAssetRule(7, user.AssetRuleInput{
		AssetType: "crypto", AssetSymbol: "btc", RuleType: "price_level", Direction: strPtr("above"), LevelUSD: floatPtr(1),
	})

	require.Error(t, err)
	assert.Zero(t, id)
}

func TestCreateAssetRule_Duplicate(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(true))

	_, err := svc.CreateAssetRule(7, user.AssetRuleInput{AssetType: "crypto", AssetSymbol: "BTC", RuleType: "pct_change_24h", Pct: floatPtr(5)})
	assert.ErrorIs(t, err, user.ErrDuplicateRule)
}

func TestCreateAssetRule_SymbolLimit(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 2)
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	mock.ExpectQuery(`COUNT\(DISTINCT asset_symbol\) FROM asset_rules`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))

	_, err := svc.CreateAssetRule(7, user.AssetRuleInput{AssetType: "crypto", AssetSymbol: "ETH", RuleType: "pct_change_24h", Pct: floatPtr(5)})
	assert.ErrorIs(t, err, user.ErrMaxUniqueSymbolsReached)
}

func TestUpdateAssetRule_OtherUsersRuleIsNotFound(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectBegin()
	mock.ExpectQuery(`FOR UPDATE`).WithArgs(99, 7).WillReturnRows(sqlmock.NewRows(lockColumns))
	mock.ExpectRollback()

	err := svc.UpdateAssetRule(7, 99, user.UpdateAssetRuleRequest{IsActive: boolPtr(false)})
	assert.ErrorIs(t, err, user.ErrRuleNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAssetRule_ToggleOffBumpsVersionKeepsState(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectBegin()
	mock.ExpectQuery(`FOR UPDATE`).WillReturnRows(sqlmock.NewRows(lockColumns).
		AddRow("crypto", "BTC", "price_level", "above", nil, 100.0, nil, true, false, "USD", nil))
	mock.ExpectExec(`UPDATE asset_rules SET is_active = \?, params_version = params_version \+ 1 WHERE id = \? AND user_id = \?`).
		WithArgs(false, 5, 7).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, svc.UpdateAssetRule(7, 5, user.UpdateAssetRuleRequest{IsActive: boolPtr(false)}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAssetRule_ReactivatingFiredTrailingResetsState(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectBegin()
	mock.ExpectQuery(`FOR UPDATE`).WillReturnRows(sqlmock.NewRows(lockColumns).
		AddRow("crypto", "BTC", "trailing_stop", nil, nil, nil, 10.0, false, true, "USD", nil))
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(true))
	mock.ExpectExec(`params_version = params_version \+ 1, baseline_pending = FALSE, last_beyond = NULL, peak_price_usd = NULL, peak_since = NULL[\s\S]*triggered_at = NULL`).
		WithArgs(true, 5, 7).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, svc.UpdateAssetRule(7, 5, user.UpdateAssetRuleRequest{IsActive: boolPtr(true)}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAssetRule_FullEditResetsState(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectBegin()
	mock.ExpectQuery(`FOR UPDATE`).WillReturnRows(sqlmock.NewRows(lockColumns).
		AddRow("crypto", "BTC", "price_level", "above", nil, 100.0, nil, true, false, "USD", nil))
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	mock.ExpectExec(`asset_type = \?, asset_symbol = \?, rule_type = \?, direction = \?, intent = \?, level_usd = \?, pct = \?, level_currency = \?, level_idr = \?, baseline_pending = FALSE`).
		WithArgs(true, "crypto", "BTC", "price_level", "above", nil, 120000.0, nil, "USD", nil, 5, 7).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := svc.UpdateAssetRule(7, 5, user.UpdateAssetRuleRequest{Rule: &user.AssetRuleInput{
		AssetType: "crypto", AssetSymbol: "BTC", RuleType: "price_level", Direction: strPtr("above"), LevelUSD: floatPtr(120000),
	}})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRearmAssetRule_OnlyFiredTrailing(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectBegin()
	mock.ExpectQuery(`FOR UPDATE`).WillReturnRows(sqlmock.NewRows(lockColumns).
		AddRow("crypto", "BTC", "price_level", "above", nil, 100.0, nil, true, false, "USD", nil))
	mock.ExpectRollback()

	assert.ErrorIs(t, svc.RearmAssetRule(7, 5), user.ErrCannotRearm)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteAssetRule_NotFound(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectExec(`DELETE FROM asset_rules WHERE id = \? AND user_id = \?`).WithArgs(99, 7).WillReturnResult(sqlmock.NewResult(0, 0))

	assert.ErrorIs(t, svc.DeleteAssetRule(7, 99), user.ErrRuleNotFound)
}

func TestGetAssetRules_MapsState(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	fired := time.Date(2026, 10, 15, 4, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`FROM asset_rules\s+WHERE user_id = \?`).WithArgs(7).WillReturnRows(sqlmock.NewRows([]string{
		"id", "asset_type", "asset_symbol", "rule_type", "direction", "intent", "level_usd", "pct", "is_active",
		"peak_price_usd", "triggered_at", "triggered_price_usd", "last_alerted_at", "created_at", "level_currency", "level_idr", "week52_ref_usd",
	}).AddRow(int64(5), "crypto", "BTC", "trailing_stop", nil, nil, nil, 10.0, false, 126080.0, fired, 113400.0, fired, fired, "USD", nil, nil))

	got, err := svc.GetAssetRules(7)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Nil(t, got[0].Direction)
	require.NotNil(t, got[0].TriggeredAt)
	assert.Equal(t, 126080.0, *got[0].PeakPriceUSD)
	assert.Equal(t, "USD", got[0].LevelCurrency)
}

func TestUpdateAssetRule_UnchangedSaveKeepsState(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectBegin()
	mock.ExpectQuery(`FOR UPDATE`).WillReturnRows(sqlmock.NewRows(lockColumns).
		AddRow("crypto", "BTC", "price_level", "above", "take_profit", 120000.0, nil, true, false, "USD", nil))
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	// Same definition as stored: no definition columns and no state reset,
	// otherwise a crossed level would alert again on the next run.
	mock.ExpectExec(`^UPDATE asset_rules SET is_active = \?, params_version = params_version \+ 1 WHERE id = \? AND user_id = \?$`).
		WithArgs(true, 5, 7).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := svc.UpdateAssetRule(7, 5, user.UpdateAssetRuleRequest{Rule: &user.AssetRuleInput{
		AssetType: "crypto", AssetSymbol: "btc", RuleType: "price_level", Intent: strPtr("take_profit"), LevelUSD: floatPtr(120000),
	}})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAssetRule_EditingFiredTrailingKeepsFiredState(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectBegin()
	mock.ExpectQuery(`FOR UPDATE`).WillReturnRows(sqlmock.NewRows(lockColumns).
		AddRow("crypto", "BTC", "trailing_stop", nil, nil, nil, 10.0, false, true, "USD", nil))
	// Still inactive after the edit: the peak and cooldown reset, but the
	// fired badge and the Re-arm button must stay, so triggered_* is kept.
	mock.ExpectExec(`baseline_pending = FALSE, last_beyond = NULL, peak_price_usd = NULL, peak_since = NULL, week52_ref_usd = NULL, last_alerted_at = NULL, last_alerted_price_usd = NULL, cooldown_until = NULL WHERE id = \? AND user_id = \?$`).
		WithArgs(false, "crypto", "BTC", "trailing_stop", nil, nil, nil, 5.0, "USD", nil, 5, 7).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := svc.UpdateAssetRule(7, 5, user.UpdateAssetRuleRequest{Rule: &user.AssetRuleInput{
		AssetType: "crypto", AssetSymbol: "BTC", RuleType: "trailing_stop", Pct: floatPtr(5),
	}})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRearmAssetRule_FiredTrailingResetsEverything(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectBegin()
	mock.ExpectQuery(`FOR UPDATE`).WillReturnRows(sqlmock.NewRows(lockColumns).
		AddRow("crypto", "BTC", "trailing_stop", nil, nil, nil, 10.0, false, true, "USD", nil))
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(true))
	mock.ExpectExec(`peak_price_usd = NULL, peak_since = NULL[\s\S]*triggered_at = NULL, triggered_price_usd = NULL`).
		WithArgs(true, 5, 7).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, svc.RearmAssetRule(7, 5))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAssetRules_FlagsUnsupportedSymbols(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	now := time.Date(2026, 10, 15, 4, 0, 0, 0, time.UTC)
	cols := []string{
		"id", "asset_type", "asset_symbol", "rule_type", "direction", "intent", "level_usd", "pct", "is_active",
		"peak_price_usd", "triggered_at", "triggered_price_usd", "last_alerted_at", "created_at", "level_currency", "level_idr", "week52_ref_usd",
	}
	mock.ExpectQuery(`FROM asset_rules`).WillReturnRows(sqlmock.NewRows(cols).
		AddRow(int64(1), "crypto", "BTC", "pct_change_24h", nil, nil, nil, 5.0, true, nil, nil, nil, nil, now, "USD", nil, 118000.0).
		// A legacy free-form symbol copied by migration 000011: it can never be fetched.
		AddRow(int64(2), "stock", "BRK-B", "pct_change_24h", nil, nil, nil, 5.0, true, nil, nil, nil, nil, now, "USD", nil, nil))

	got, err := svc.GetAssetRules(7)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.True(t, got[0].Supported)
	assert.False(t, got[1].Supported)
	// #52: the 52-week reference the engine compares with is returned.
	require.NotNil(t, got[0].Week52RefUSD)
	assert.Equal(t, 118000.0, *got[0].Week52RefUSD)
	assert.Nil(t, got[1].Week52RefUSD)
}

func TestCreateAssetRule_IDRLevel(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectQuery(`SELECT EXISTS\(\s*SELECT 1 FROM asset_rules\s*WHERE user_id = \?`).
		WithArgs(7, "ANTAM", "price_level", "below", "buy_target", nil, nil, "IDR", 1350000.0, 0).
		WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	mock.ExpectQuery(`asset_symbol = \? AND is_active = true\)`).WithArgs("ANTAM").
		WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(true))
	mock.ExpectExec(`INSERT INTO asset_rules \(user_id, asset_type, asset_symbol, rule_type, direction, intent, level_usd, pct, level_currency, level_idr\)`).
		WithArgs(7, "gold", "ANTAM", "price_level", "below", "buy_target", nil, nil, "IDR", 1350000.0).
		WillReturnResult(sqlmock.NewResult(43, 1))

	id, err := svc.CreateAssetRule(7, user.AssetRuleInput{
		AssetType: "gold", AssetSymbol: "ANTAM", RuleType: "price_level", Intent: strPtr("buy_target"), LevelIDR: floatPtr(1350000),
	})

	require.NoError(t, err)
	assert.Equal(t, uint64(43), id)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Review focus 1: same number, other currency, is a new definition. The
// baseline must reset, or last_beyond from the USD comparison is reused.
func TestUpdateAssetRule_CurrencyChangeResetsState(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectBegin()
	mock.ExpectQuery(`FOR UPDATE`).WillReturnRows(sqlmock.NewRows(lockColumns).
		AddRow("crypto", "BTC", "price_level", "above", nil, 1350000.0, nil, true, false, "USD", nil))
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	mock.ExpectExec(`level_currency = \?, level_idr = \?, baseline_pending = FALSE`).
		WithArgs(true, "crypto", "BTC", "price_level", "above", nil, nil, nil, "IDR", 1350000.0, 5, 7).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := svc.UpdateAssetRule(7, 5, user.UpdateAssetRuleRequest{Rule: &user.AssetRuleInput{
		AssetType: "crypto", AssetSymbol: "BTC", RuleType: "price_level", Direction: strPtr("above"), LevelIDR: floatPtr(1350000),
	}})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAssetRule_UnchangedIDRSaveKeepsState(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectBegin()
	mock.ExpectQuery(`FOR UPDATE`).WillReturnRows(sqlmock.NewRows(lockColumns).
		AddRow("gold", "ANTAM", "price_level", "below", "buy_target", nil, nil, true, false, "IDR", 1350000.0))
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	mock.ExpectExec(`UPDATE asset_rules SET is_active = \?, params_version = params_version \+ 1 WHERE id = \? AND user_id = \?`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := svc.UpdateAssetRule(7, 5, user.UpdateAssetRuleRequest{Rule: &user.AssetRuleInput{
		AssetType: "gold", AssetSymbol: "ANTAM", RuleType: "price_level", Intent: strPtr("buy_target"), LevelIDR: floatPtr(1350000),
	}})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
