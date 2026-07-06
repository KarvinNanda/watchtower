package scheduler

import (
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/asset"
	"github.com/karvin-nanda/watchtower/internal/notifier"
)

func floatPtr(v float64) *float64    { return &v }
func timePtr(t time.Time) *time.Time { return &t }

type mockAssetNotifierCall struct {
	ChatID  int64
	Message string
}

// mockAssetNotifier is a NotifierInterface test double that just records
// every call, so tests can assert on how many Telegram messages a batch
// dispatch actually sent — the crux of the "combined message, not one per
// symbol" behavior under test here.
type mockAssetNotifier struct {
	calls []mockAssetNotifierCall
}

func (m *mockAssetNotifier) SendAssetAlert(chatID int64, message string) error {
	m.calls = append(m.calls, mockAssetNotifierCall{ChatID: chatID, Message: message})
	return nil
}

// newMockAssetWorker builds an AssetWorker with only the fields exercised
// by the batching/dispatch logic under test — db (sqlmock-backed), notifier
// (recording mock), notifLogs, and alertPriceMovePct. cache/redis/analyzer
// are left nil since evaluateSubscriber/appendTriggeredAlerts/dispatchBatches
// never touch them.
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
	}
	return w, mock, mockNotif
}

func sampleSubscriber(userID uint64, priceUpper *float64) SubscriberData {
	return SubscriberData{
		UserID:              userID,
		AlertType:           "price_threshold",
		PriceUpperUSD:       priceUpper,
		TelegramAssetChatID: int64(userID) * 1000,
		AlertCooldownHours:  4,
		PreferredLanguage:   "id",
	}
}

func sampleFetchResultFor(symbol string, price float64) *asset.FetchResult {
	return &asset.FetchResult{
		Symbol:       symbol,
		PriceUSD:     price,
		ChangePct24h: 1.0,
		Source:       "test",
		FetchedAt:    time.Now(),
	}
}

func TestBatchInitializedOnce(t *testing.T) {
	t.Parallel()
	w, _, _ := newMockAssetWorker(t, 5.0)

	batches := make(map[uint64]*UserAlertBatch)
	cooldownHoursByUser := make(map[uint64]int)

	// The same 2 subscribers are present for all 3 symbols below — this
	// proves batches accumulates across calls (as it does across symbols
	// within one AssetWorker.Run) rather than being recreated per symbol,
	// which was one of the two hypothesized causes of the "BTC sent twice"
	// bug (see appendTriggeredAlerts's caller, Run(), which creates batches
	// exactly once before its symbol loop).
	sub1 := sampleSubscriber(1, floatPtr(100))
	sub2 := sampleSubscriber(2, floatPtr(100))
	subscribers := []SubscriberData{sub1, sub2}

	for _, symbol := range []string{"BTC", "ETH", "SOL"} {
		fetchResult := sampleFetchResultFor(symbol, 150) // breaches PriceUpperUSD=100 for both subscribers
		triggered := w.appendTriggeredAlerts(subscribers, fetchResult, 150*16000, nil, batches, cooldownHoursByUser)
		assert.Equal(t, 2, triggered, "both subscribers should trigger for %s", symbol)
	}

	require.Len(t, batches, 2, "batches map must have exactly one entry per user, not per symbol")
	require.Contains(t, batches, uint64(1))
	require.Contains(t, batches, uint64(2))
	assert.Len(t, batches[1].Alerts, 3, "user 1 should accumulate one alert per symbol across all 3 calls")
	assert.Len(t, batches[2].Alerts, 3, "user 2 should accumulate one alert per symbol across all 3 calls")

	// Verify dispatch actually sends ONE combined Telegram message
	// covering all 3 symbols for user 1, not 3 separate messages.
	w2, mock, mockNotif := newMockAssetWorker(t, 5.0)
	mock.ExpectExec(`INSERT INTO alert_states`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO alert_states`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO alert_states`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO notification_logs`).WillReturnResult(sqlmock.NewResult(1, 1))

	w2.dispatchUserBatch(batches[1], cooldownHoursByUser[1], "01 Jan 2026 00:00 WIB", time.Now())

	require.Len(t, mockNotif.calls, 1, "3 triggered symbols for one user must produce exactly one combined message")
	assert.Contains(t, mockNotif.calls[0].Message, "BTC")
	assert.Contains(t, mockNotif.calls[0].Message, "ETH")
	assert.Contains(t, mockNotif.calls[0].Message, "SOL")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchNoDuplicateUsers(t *testing.T) {
	t.Parallel()
	w, _, _ := newMockAssetWorker(t, 5.0)

	batches := make(map[uint64]*UserAlertBatch)
	cooldownHoursByUser := make(map[uint64]int)

	sub := sampleSubscriber(1, floatPtr(100))

	// BTC triggers, then NVDA triggers, both for the same user.
	w.appendTriggeredAlerts([]SubscriberData{sub}, sampleFetchResultFor("BTC", 150), 150*16000, nil, batches, cooldownHoursByUser)
	w.appendTriggeredAlerts([]SubscriberData{sub}, sampleFetchResultFor("NVDA", 150), 150*16000, nil, batches, cooldownHoursByUser)

	require.Len(t, batches, 1, "one user triggering on two symbols must produce exactly one map entry, not two")
	assert.Len(t, batches[1].Alerts, 2, "that one entry must contain both AlertItems")

	symbols := []string{batches[1].Alerts[0].Symbol, batches[1].Alerts[1].Symbol}
	assert.ElementsMatch(t, []string{"BTC", "NVDA"}, symbols)
}

func TestBatchEmptyWhenNoTrigger(t *testing.T) {
	t.Parallel()
	w, _, _ := newMockAssetWorker(t, 5.0)

	batches := make(map[uint64]*UserAlertBatch)
	cooldownHoursByUser := make(map[uint64]int)

	// Price stays within every subscriber's threshold.
	sub := sampleSubscriber(1, floatPtr(200))
	fetchResult := sampleFetchResultFor("BTC", 150)

	triggered := w.appendTriggeredAlerts([]SubscriberData{sub}, fetchResult, 150*16000, nil, batches, cooldownHoursByUser)

	assert.Equal(t, 0, triggered)
	assert.Empty(t, batches, "no message should be queued when no subscriber's threshold is breached")
}

func TestBatchCooldownRespected(t *testing.T) {
	t.Parallel()
	w := &AssetWorker{alertPriceMovePct: 5.0}

	lastAlertedPrice := 50000.0
	subscriber := SubscriberData{
		UserID:              1,
		AlertType:           "price_threshold",
		PriceUpperUSD:       floatPtr(50000),
		TelegramAssetChatID: 1000,
		AlertCooldownHours:  4,
		PreferredLanguage:   "id",
		LastAlertedPriceUSD: &lastAlertedPrice,
		// Cooldown (4h) alerted 1h ago -> still 3h remaining.
		CooldownUntil: timePtr(time.Now().Add(3 * time.Hour)),
	}

	// Price is still beyond the threshold, but has only moved 0.2% since
	// the last alert -- well under alertPriceMovePct (5%) -- so the
	// cooldown should still suppress it.
	fetchResult := sampleFetchResultFor("BTC", 50100)

	item, triggered, err := w.evaluateSubscriber(subscriber, fetchResult, 50100*16000, nil)

	require.NoError(t, err)
	assert.False(t, triggered, "cooldown should suppress an alert when price hasn't moved enough to bypass it")
	assert.Equal(t, AlertItem{}, item)
}

func TestBatchCooldownBypassedByPriceMove(t *testing.T) {
	t.Parallel()
	w := &AssetWorker{alertPriceMovePct: 5.0}

	lastAlertedPrice := 50000.0
	subscriber := SubscriberData{
		UserID:              1,
		AlertType:           "price_threshold",
		PriceUpperUSD:       floatPtr(50000),
		TelegramAssetChatID: 1000,
		AlertCooldownHours:  4,
		PreferredLanguage:   "id",
		LastAlertedPriceUSD: &lastAlertedPrice,
		CooldownUntil:       timePtr(time.Now().Add(3 * time.Hour)),
	}

	// Price has moved 6% since the last alert -- over alertPriceMovePct
	// (5%) -- so the alert should fire despite the active cooldown.
	fetchResult := sampleFetchResultFor("BTC", 53000)

	item, triggered, err := w.evaluateSubscriber(subscriber, fetchResult, 53000*16000, nil)

	require.NoError(t, err)
	require.True(t, triggered, "a price move beyond alertPriceMovePct should bypass an active cooldown")
	assert.Equal(t, "BTC", item.Symbol)
	assert.Equal(t, "upper", item.TriggerType)
}
