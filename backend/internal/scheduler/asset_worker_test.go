package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/cache"
	"github.com/karvin-nanda/watchtower/internal/notifier"
	"github.com/karvin-nanda/watchtower/internal/rules"
)

func floatPtr(v float64) *float64 { return &v }
func boolPtr(v bool) *bool        { return &v }

type mockAssetNotifierCall struct {
	ChatID  int64
	Message string
}

// mockAssetNotifier records every send; err makes every send fail.
type mockAssetNotifier struct {
	calls []mockAssetNotifierCall
	err   error
}

func (m *mockAssetNotifier) SendAssetAlert(chatID int64, message string) error {
	m.calls = append(m.calls, mockAssetNotifierCall{ChatID: chatID, Message: message})
	return m.err
}

// fakeMarket is a marketSource test double.
type fakeMarket struct {
	data      map[string]*cache.MarketData
	week52Err error
}

func (f *fakeMarket) GetMarketData(_ context.Context, symbol, _ string) (*cache.MarketData, error) {
	d, ok := f.data[symbol]
	if !ok {
		return nil, errors.New("no market data")
	}
	return d, nil
}

func (f *fakeMarket) GetCryptoWeek52(_ context.Context, _ string) (float64, float64, error) {
	if f.week52Err != nil {
		return 0, 0, f.week52Err
	}
	return 120, 50, nil
}

func newMockAssetWorker(t *testing.T, alertPriceMovePct float64) (*AssetWorker, sqlmock.Sqlmock, *mockAssetNotifier) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mockNotif := &mockAssetNotifier{}
	w := &AssetWorker{
		db:                db,
		notifier:          mockNotif,
		notifLogs:         notifier.NewLogRepository(db),
		alertPriceMovePct: alertPriceMovePct,
		maxUniqueSymbols:  100,
	}
	return w, mock, mockNotif
}

// levelRow is a fresh (never evaluated) "above level" rule for userID.
func levelRow(ruleID, userID uint64, level float64) ruleRow {
	return ruleRow{
		Rule:                rules.Rule{ID: ruleID, Type: rules.TypePriceLevel, Direction: rules.DirectionAbove, LevelUSD: level},
		UserID:              userID,
		ParamsVersion:       1,
		TelegramAssetChatID: int64(userID) * 1000,
		AlertCooldownHours:  4,
		PreferredLanguage:   "en",
	}
}

func TestEvaluateRules_OneBatchPerUserAcrossSymbols(t *testing.T) {
	t.Parallel()
	w, _, _ := newMockAssetWorker(t, 3)
	batches := make(map[uint64]*UserAlertBatch)

	for i, symbol := range []string{"BTC", "ETH", "SOL"} {
		rows := []ruleRow{levelRow(uint64(10+i), 1, 100), levelRow(uint64(20+i), 2, 100)}
		triggered := w.evaluateRules(context.Background(), rows, symbol, rules.Market{PriceUSD: 150}, 150*16000, runScope{}, batches)
		assert.Equal(t, 2, triggered, symbol)
	}

	require.Len(t, batches, 2, "one batch per user, not per symbol")
	assert.Len(t, batches[1].Alerts, 3)
	assert.Len(t, batches[2].Alerts, 3)
}

func TestEvaluateRules_TwoRulesOnOneSymbolBothFire(t *testing.T) {
	t.Parallel()
	w, _, _ := newMockAssetWorker(t, 3)
	batches := make(map[uint64]*UserAlertBatch)
	tp := levelRow(1, 1, 100)
	tp.Rule.Intent = rules.IntentTakeProfit
	plain := levelRow(2, 1, 120)

	triggered := w.evaluateRules(context.Background(), []ruleRow{tp, plain}, "BTC", rules.Market{PriceUSD: 130}, 0, runScope{}, batches)

	assert.Equal(t, 2, triggered)
	require.Len(t, batches[1].Alerts, 2)
	assert.Equal(t, uint64(1), batches[1].Alerts[0].RuleID)
	assert.Equal(t, uint64(2), batches[1].Alerts[1].RuleID)
}

func TestEvaluateRules_PersistsObservedStateWithoutFiring(t *testing.T) {
	t.Parallel()
	w, mock, _ := newMockAssetWorker(t, 3)
	mock.ExpectExec(`UPDATE asset_rules SET last_beyond = \? WHERE id = \? AND params_version = \?`).
		WithArgs(false, 5, 1).
		WillReturnResult(sqlmock.NewResult(0, 1))
	batches := make(map[uint64]*UserAlertBatch)

	triggered := w.evaluateRules(context.Background(), []ruleRow{levelRow(5, 1, 100)}, "BTC", rules.Market{PriceUSD: 90}, 0, runScope{}, batches)

	assert.Equal(t, 0, triggered)
	assert.Empty(t, batches)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEvaluateRules_ManualRunSkipsCooldown(t *testing.T) {
	t.Parallel()
	w, _, _ := newMockAssetWorker(t, 3)
	row := levelRow(5, 1, 100)
	until := time.Now().Add(3 * time.Hour)
	row.Rule.State = rules.State{LastBeyond: boolPtr(false), LastAlertedPriceUSD: floatPtr(104), CooldownUntil: &until}

	periodic := w.evaluateRules(context.Background(), []ruleRow{row}, "BTC", rules.Market{PriceUSD: 105}, 0, runScope{}, map[uint64]*UserAlertBatch{})
	manual := w.evaluateRules(context.Background(), []ruleRow{row}, "BTC", rules.Market{PriceUSD: 105}, 0, runScope{userID: 1, skipCooldown: true}, map[uint64]*UserAlertBatch{})

	assert.Equal(t, 0, periodic, "cooldown holds a periodic run")
	assert.Equal(t, 1, manual, "Check now ignores cooldown")
}

func TestDispatchUserBatch_AppliesOnSentAfterSuccess(t *testing.T) {
	t.Parallel()
	w, mock, notif := newMockAssetWorker(t, 3)
	batches := make(map[uint64]*UserAlertBatch)
	w.evaluateRules(context.Background(), []ruleRow{levelRow(1, 1, 100), levelRow(2, 1, 100)}, "BTC", rules.Market{PriceUSD: 150}, 0, runScope{}, batches)

	mock.ExpectExec(`UPDATE asset_rules SET last_beyond = \?, last_alerted_at`).WithArgs(true, sqlmock.AnyArg(), 150.0, sqlmock.AnyArg(), 1, 1).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE asset_rules SET last_beyond = \?, last_alerted_at`).WithArgs(true, sqlmock.AnyArg(), 150.0, sqlmock.AnyArg(), 2, 1).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO notification_logs`).WillReturnResult(sqlmock.NewResult(1, 1))

	ok := w.dispatchUserBatch(batches[1], "01 Jan 2026 00:00 WIB")

	assert.True(t, ok)
	require.Len(t, notif.calls, 1, "two rules for one user go into one message")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDispatchUserBatch_FailedSendKeepsState(t *testing.T) {
	t.Parallel()
	w, mock, notif := newMockAssetWorker(t, 3)
	notif.err = errors.New("telegram down")
	batches := make(map[uint64]*UserAlertBatch)
	w.evaluateRules(context.Background(), []ruleRow{levelRow(1, 1, 100)}, "BTC", rules.Market{PriceUSD: 150}, 0, runScope{}, batches)

	// sqlmock does not report unexpected calls, and applyPatch only logs its
	// error, so "no UPDATE expected" alone proves nothing. Instead, set a trap:
	// an UPDATE expectation that must stay unmatched after a failed send.
	mock.MatchExpectationsInOrder(false)
	mock.ExpectExec(`UPDATE asset_rules`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO notification_logs`).WillReturnResult(sqlmock.NewResult(1, 1))

	ok := w.dispatchUserBatch(batches[1], "01 Jan 2026 00:00 WIB")

	assert.False(t, ok)
	err := mock.ExpectationsWereMet()
	require.Error(t, err, "OnSent was written although the send failed: the alert would be lost")
	assert.Contains(t, err.Error(), "UPDATE asset_rules", "only the UPDATE trap may stay unmatched")
}

func TestProcessSymbol_CryptoWeek52FailureDoesNotBlockPriceRules(t *testing.T) {
	t.Parallel()
	w, mock, _ := newMockAssetWorker(t, 3)
	w.cache = &fakeMarket{
		data:      map[string]*cache.MarketData{"BTC": {Symbol: "BTC", PriceUSD: 150, PriceIDR: 2400000}},
		week52Err: errors.New("coingecko 429"),
	}
	mock.ExpectQuery(`FROM asset_rules r`).WithArgs("BTC").WillReturnRows(sqlmock.NewRows(ruleColumns).
		AddRow(int64(1), int64(1), "price_level", "above", nil, 100.0, nil, int64(1), false, nil, nil, nil, nil, nil, int64(1000), int64(4), "en").
		AddRow(int64(2), int64(1), "week52_break", "above", nil, nil, nil, int64(1), false, nil, nil, nil, nil, nil, int64(1000), int64(4), "en"))
	batches := make(map[uint64]*UserAlertBatch)

	res, err := w.processSymbol(SymbolInfo{Symbol: "BTC", AssetType: "crypto", NeedsWeek52: true}, runScope{}, batches)

	require.NoError(t, err)
	assert.Equal(t, symbolResult{checked: 2, triggered: 1, dataErrors: 1}, res)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRun_OneFailingSymbolDoesNotBlockOthers(t *testing.T) {
	w, mock, _ := newMockAssetWorker(t, 3)
	w.cache = &fakeMarket{data: map[string]*cache.MarketData{"ETH": {Symbol: "ETH", PriceUSD: 90, PriceIDR: 1}}}
	mock.ExpectQuery(`SELECT asset_symbol, MIN\(asset_type\)`).WillReturnRows(
		sqlmock.NewRows([]string{"asset_symbol", "asset_type", "needs_week52"}).
			AddRow("BRK-B", "stock", int64(0)).
			AddRow("ETH", "crypto", int64(0)))
	mock.ExpectQuery(`FROM asset_rules r`).WithArgs("ETH").WillReturnRows(sqlmock.NewRows(ruleColumns).
		AddRow(int64(3), int64(1), "price_level", "above", nil, 100.0, nil, int64(1), false, nil, nil, nil, nil, nil, int64(1000), int64(4), "en"))
	mock.ExpectExec(`UPDATE asset_rules SET last_beyond = \?`).WillReturnResult(sqlmock.NewResult(0, 1))

	res, err := w.run(runScope{})

	require.NoError(t, err)
	assert.Equal(t, AssetRunResult{RulesChecked: 1, Errors: 1}, res)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRunForUser_WaitsForLockAndHandlesNoRules(t *testing.T) {
	w, mock, _ := newMockAssetWorker(t, 3)
	mock.ExpectQuery(`FROM asset_rules[\s\S]*AND user_id = \?`).
		WithArgs(7, 100).
		WillReturnRows(sqlmock.NewRows([]string{"asset_symbol", "asset_type", "needs_week52"}))

	w.mu.Lock()
	done := make(chan AssetRunResult, 1)
	go func() {
		res, err := w.RunForUser(7)
		assert.NoError(t, err)
		done <- res
	}()

	select {
	case <-done:
		t.Fatal("RunForUser ran while another asset run held the lock")
	case <-time.After(100 * time.Millisecond):
	}
	w.mu.Unlock()

	select {
	case res := <-done:
		assert.Equal(t, AssetRunResult{}, res)
	case <-time.After(2 * time.Second):
		t.Fatal("RunForUser never ran after the lock was released")
	}
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRun_ReturnsErrorWhenEverySymbolFailed(t *testing.T) {
	w, mock, _ := newMockAssetWorker(t, 3)
	w.cache = &fakeMarket{}
	mock.ExpectQuery(`SELECT asset_symbol, MIN\(asset_type\)`).
		WillReturnRows(sqlmock.NewRows([]string{"asset_symbol", "asset_type", "needs_week52"}).AddRow("BTC", "crypto", int64(0)))

	assert.Error(t, w.Run(), "a periodic run where nothing could be fetched must be retried")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBuildAssetItemBlock_FourLines(t *testing.T) {
	t.Parallel()
	block := buildAssetItemBlock("en", AlertItem{
		Symbol: "BTC", PriceUSD: 65000, PriceIDR: 65000 * 16000, ChangePct: floatPtr(-3.2),
		Trigger: rules.Trigger{Type: rules.TypePriceLevel, Direction: rules.DirectionAbove, Intent: rules.IntentTakeProfit, LevelUSD: 64000},
	})
	assert.Contains(t, block, "BTC")
	assert.Contains(t, block, `\-3\.20%`)
	// "$" is not a MarkdownV2 special character; "-", "(", ")" and "." are.
	assert.Contains(t, block, `Reached your take\-profit level \($64,000\.00\)`)
	assert.Equal(t, 3, strings.Count(block, "\n"), "symbol, price, 24h, trigger")
}

func TestBuildAssetItemBlock_UnknownChangeIsNA(t *testing.T) {
	t.Parallel()
	block := buildAssetItemBlock("id", AlertItem{Symbol: "ANTAM", PriceUSD: 146, PriceIDR: 2392157,
		Trigger: rules.Trigger{Type: rules.TypePctChange24h, Pct: 2}})
	assert.Contains(t, block, "24h: n/a")
}

func TestTriggerLine(t *testing.T) {
	cases := []struct {
		lang string
		trig rules.Trigger
		want string
	}{
		{"en", rules.Trigger{Type: rules.TypePriceLevel, Direction: rules.DirectionBelow, Intent: rules.IntentBuyTarget, LevelUSD: 90000}, "Reached your buy target ($90,000.00)"},
		{"id", rules.Trigger{Type: rules.TypePriceLevel, Direction: rules.DirectionBelow, Intent: rules.IntentStopLoss, LevelUSD: 85000}, "Mencapai level stop-loss kamu ($85,000.00)"},
		{"en", rules.Trigger{Type: rules.TypePriceLevel, Direction: rules.DirectionAbove, LevelUSD: 120000}, "Above $120,000.00"},
		{"id", rules.Trigger{Type: rules.TypePriceLevel, Direction: rules.DirectionBelow, LevelUSD: 1}, "Di bawah $1.00"},
		{"en", rules.Trigger{Type: rules.TypePctChange24h, Pct: 5}, "24h change beyond ±5%"},
		{"id", rules.Trigger{Type: rules.TypePctChange24h, Pct: 2.5}, "Perubahan 24 jam melewati ±2.5%"},
		{"en", rules.Trigger{Type: rules.TypeTrailingStop, Pct: 10, PeakPriceUSD: 126080}, "Down 10% or more from peak $126,080.00"},
		{"id", rules.Trigger{Type: rules.TypeTrailingStop, Pct: 10, PeakPriceUSD: 100}, "Turun 10% atau lebih dari peak $100.00"},
		{"en", rules.Trigger{Type: rules.TypeWeek52Break, Direction: rules.DirectionAbove, PrevRefUSD: 348.15}, "New 52-week high (previous $348.15)"},
		{"id", rules.Trigger{Type: rules.TypeWeek52Break, Direction: rules.DirectionBelow, PrevRefUSD: 243.42}, "52-week low baru (sebelumnya $243.42)"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, triggerLine(tc.lang, tc.trig))
	}
}

func TestFormatUSD_SmallPrices(t *testing.T) {
	assert.Equal(t, "$61,633.00", formatUSD(61633))
	assert.Equal(t, "$0.5", formatUSD(0.5))
	assert.Equal(t, "$0.00001234", formatUSD(0.00001234))
}
