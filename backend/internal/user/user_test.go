package user_test

import (
	"database/sql"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/user"
)

func newMockUserService(t *testing.T, maxUniqueSymbols int) (*user.UserService, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return user.NewUserService(db, maxUniqueSymbols), mock
}

func floatPtr(v float64) *float64 { return &v }

// validAssetSubRequest returns a request that passes every validation rule
// on its own; individual tests mutate a copy of it to isolate one failure
// condition at a time.
func validAssetSubRequest() user.CreateAssetSubRequest {
	return user.CreateAssetSubRequest{
		AssetType:          "crypto",
		AssetSymbol:        "BTC",
		AlertType:          "both",
		PriceLowerUSD:      floatPtr(50000),
		PriceUpperUSD:      floatPtr(150000),
		PctChangeThreshold: floatPtr(5),
	}
}

func TestCreateAssetSubscription_Validation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		mutate  func(r *user.CreateAssetSubRequest)
		wantErr error
	}{
		{
			name:    "invalid_asset_type",
			mutate:  func(r *user.CreateAssetSubRequest) { r.AssetType = "invalid" },
			wantErr: user.ErrInvalidAssetType,
		},
		{
			name:    "invalid_alert_type",
			mutate:  func(r *user.CreateAssetSubRequest) { r.AlertType = "invalid" },
			wantErr: user.ErrInvalidAlertType,
		},
		{
			name: "price_threshold_without_bounds",
			mutate: func(r *user.CreateAssetSubRequest) {
				r.AlertType = "price_threshold"
				r.PriceLowerUSD = nil
				r.PriceUpperUSD = nil
			},
			wantErr: user.ErrMissingPriceThreshold,
		},
		{
			name: "pct_change_without_threshold",
			mutate: func(r *user.CreateAssetSubRequest) {
				r.AlertType = "pct_change"
				r.PctChangeThreshold = nil
			},
			wantErr: user.ErrMissingPctThreshold,
		},
		{
			name: "negative_price_lower",
			mutate: func(r *user.CreateAssetSubRequest) {
				r.AlertType = "price_threshold"
				r.PriceLowerUSD = floatPtr(-100)
			},
			wantErr: user.ErrInvalidPriceValue,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, _ := newMockUserService(t, 100)
			req := validAssetSubRequest()
			tc.mutate(&req)

			// None of these cases should ever reach the database: every
			// one is rejected by in-memory validation first.
			err := svc.CreateAssetSubscription(1, req)
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}

	t.Run("valid_request_succeeds", func(t *testing.T) {
		t.Parallel()
		svc, mock := newMockUserService(t, 100)
		req := validAssetSubRequest()

		mock.ExpectQuery(`SELECT EXISTS`).
			WithArgs("BTC").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectQuery(`SELECT COUNT\(DISTINCT asset_symbol\)`).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectExec(`INSERT INTO asset_subscriptions`).
			WithArgs(uint64(1), "crypto", "BTC", "both", floatPtr(50000), floatPtr(150000), floatPtr(5)).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := svc.CreateAssetSubscription(1, req)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCreateAssetSubscription_SymbolLimit(t *testing.T) {
	t.Parallel()
	const maxUniqueSymbols = 3
	svc, mock := newMockUserService(t, maxUniqueSymbols)

	req := validAssetSubRequest()
	req.AssetSymbol = "ETH" // a symbol not already tracked by anyone

	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("ETH").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(`SELECT COUNT\(DISTINCT asset_symbol\)`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(maxUniqueSymbols))

	err := svc.CreateAssetSubscription(1, req)

	assert.ErrorIs(t, err, user.ErrMaxUniqueSymbolsReached)
	require.NoError(t, mock.ExpectationsWereMet(), "no INSERT should run once the symbol cap is hit")
}

func TestCreateKeywordSubscription_Duplicate(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)

	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs(uint64(1), "android").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec(`INSERT INTO keyword_subscriptions`).
		WithArgs(uint64(1), "android", nil).
		WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(t, svc.CreateKeywordSubscription(1, user.CreateKeywordSubRequest{Keyword: "android"}))

	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs(uint64(1), "android").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	err := svc.CreateKeywordSubscription(1, user.CreateKeywordSubRequest{Keyword: "android"})

	assert.ErrorIs(t, err, user.ErrDuplicateKeyword)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateKeywordSubscription_Sanitize(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)

	// The mocked args below only match if the service actually sanitizes
	// "  ANDROID  " down to "android" before it ever reaches a query.
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs(uint64(1), "android").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec(`INSERT INTO keyword_subscriptions`).
		WithArgs(uint64(1), "android", nil).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := svc.CreateKeywordSubscription(1, user.CreateKeywordSubRequest{Keyword: "  ANDROID  "})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func int64Ptr(v int64) *int64 { return &v }
func intPtr(v int) *int       { return &v }
func strPtr(v string) *string { return &v }
func boolPtr(v bool) *bool    { return &v }

func TestUpdateProfile_Validation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		req     user.UpdateProfileRequest
		wantErr error
	}{
		{
			name:    "invalid_expertise_level",
			req:     user.UpdateProfileRequest{ExpertiseLevel: strPtr("wizard")},
			wantErr: user.ErrInvalidExpertiseLevel,
		},
		{
			name:    "invalid_preferred_language",
			req:     user.UpdateProfileRequest{PreferredLanguage: strPtr("fr")},
			wantErr: user.ErrInvalidPreferredLang,
		},
		{
			name:    "non_positive_telegram_asset_chat_id",
			req:     user.UpdateProfileRequest{TelegramAssetChatID: int64Ptr(-5)},
			wantErr: user.ErrInvalidTelegramChatID,
		},
		{
			name:    "non_positive_telegram_sentinel_chat_id",
			req:     user.UpdateProfileRequest{TelegramSentinelChatID: int64Ptr(0)},
			wantErr: user.ErrInvalidTelegramChatID,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, _ := newMockUserService(t, 100)
			// Every case here must fail validation before a transaction
			// is ever opened.
			err := svc.UpdateProfile(1, tc.req)
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}

	t.Run("valid_update_succeeds", func(t *testing.T) {
		t.Parallel()
		svc, mock := newMockUserService(t, 100)

		mock.ExpectBegin()
		mock.ExpectExec(`UPDATE users SET`).
			WithArgs(int64(123), 8, uint64(1)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(`UPDATE user_profiles SET`).
			WithArgs("advanced", uint64(1)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		req := user.UpdateProfileRequest{
			TelegramAssetChatID: int64Ptr(123),
			AlertCooldownHours:  intPtr(8),
			ExpertiseLevel:      strPtr("advanced"),
		}
		err := svc.UpdateProfile(1, req)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestGetProfile_Success(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)

	rows := sqlmock.NewRows([]string{
		"id", "email", "telegram_asset_chat_id", "telegram_sentinel_chat_id",
		"alert_cooldown_hours", "preferred_language", "devices", "os_list", "expertise_level",
	}).AddRow(1, "profile@example.com", nil, nil, 4, "id", nil, nil, "beginner")
	mock.ExpectQuery(`SELECT`).WithArgs(uint64(1)).WillReturnRows(rows)

	profile, err := svc.GetProfile(1)

	require.NoError(t, err)
	assert.Equal(t, "profile@example.com", profile.Email)
	assert.Equal(t, "beginner", profile.ExpertiseLevel)
	assert.Empty(t, profile.Devices)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetProfile_NotFound(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)

	mock.ExpectQuery(`SELECT`).WithArgs(uint64(999)).WillReturnError(sql.ErrNoRows)

	_, err := svc.GetProfile(999)

	assert.ErrorIs(t, err, user.ErrUserNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteKeywordSubscription_Ownership(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)

	const userA, userB, subID = uint64(1), uint64(2), uint64(50)

	mock.ExpectQuery(`SELECT user_id FROM keyword_subscriptions`).
		WithArgs(subID).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(userA))

	err := svc.DeleteKeywordSubscription(userB, subID)

	assert.ErrorIs(t, err, user.ErrSubscriptionNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOwnershipCheck_AssetSub(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)

	const userA, userB, subID = uint64(1), uint64(2), uint64(100)

	mock.ExpectQuery(`SELECT user_id FROM asset_subscriptions`).
		WithArgs(subID).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(userA))

	err := svc.DeleteAssetSubscription(userB, subID)

	assert.ErrorIs(t, err, user.ErrSubscriptionNotFound,
		"deleting another user's subscription must fail the same way as a missing one, never revealing it exists")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAssetSubscriptions_Success(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)

	rows := sqlmock.NewRows([]string{
		"id", "asset_type", "asset_symbol", "alert_type",
		"price_lower_usd", "price_upper_usd", "pct_change_threshold", "is_active", "created_at",
	}).
		AddRow(1, "crypto", "BTC", "both", 50000.0, 150000.0, 5.0, true, time.Now()).
		AddRow(2, "stock", "AAPL", "pct_change", nil, nil, 3.0, true, time.Now())
	mock.ExpectQuery(`SELECT id, asset_type, asset_symbol`).WithArgs(uint64(1)).WillReturnRows(rows)

	subs, err := svc.GetAssetSubscriptions(1)

	require.NoError(t, err)
	require.Len(t, subs, 2)
	assert.Equal(t, "BTC", subs[0].AssetSymbol)
	require.NotNil(t, subs[0].PriceLowerUSD)
	assert.Equal(t, 50000.0, *subs[0].PriceLowerUSD)
	assert.Nil(t, subs[1].PriceLowerUSD, "a NULL price_lower_usd must decode to a nil pointer, not zero")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetKeywordSubscriptions_Success(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)

	rows := sqlmock.NewRows([]string{"id", "keyword", "context_note", "is_active", "created_at"}).
		AddRow(1, "android", nil, true, time.Now())
	mock.ExpectQuery(`SELECT id, keyword`).WithArgs(uint64(1)).WillReturnRows(rows)

	subs, err := svc.GetKeywordSubscriptions(1)

	require.NoError(t, err)
	require.Len(t, subs, 1)
	assert.Equal(t, "android", subs[0].Keyword)
	assert.Nil(t, subs[0].ContextNote)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetMarketSnapshot_Success(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)

	rows := sqlmock.NewRows([]string{"symbol", "price_usd", "price_idr", "change_pct_24h", "last_fetched", "source"}).
		AddRow("BTC", 65000.0, 1040000000.0, 2.5, time.Now(), "coingecko")
	mock.ExpectQuery(`SELECT mc.symbol`).WillReturnRows(rows)

	data, err := svc.GetMarketSnapshot()

	require.NoError(t, err)
	require.Len(t, data, 1)
	assert.Equal(t, "BTC", data[0].Symbol)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetNotificationHistory_Success(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM notification_logs`).
		WithArgs(uint64(1), "asset").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT id, notif_type`).
		WithArgs(uint64(1), "asset", 10, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "notif_type", "asset_symbol", "keyword", "content_summary", "sent_at", "status",
		}).AddRow(1, "asset", "BTC", nil, "price alert", time.Now(), "sent"))

	logs, total, err := svc.GetNotificationHistory(1, 10, 0, "asset")

	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, logs, 1)
	assert.Equal(t, "asset", logs[0].NotifType)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetLastAlertTimestamps_Success(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)

	now := time.Now()
	mock.ExpectQuery(`SELECT MAX\(sent_at\) FROM notification_logs WHERE user_id = \? AND notif_type = 'asset'`).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(now))
	mock.ExpectQuery(`SELECT MAX\(sent_at\) FROM notification_logs WHERE user_id = \? AND notif_type = 'sentinel'`).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(nil))

	lastAsset, lastSentinel, err := svc.GetLastAlertTimestamps(1)

	require.NoError(t, err)
	require.NotNil(t, lastAsset)
	assert.Nil(t, lastSentinel, "no sentinel alert has ever been sent, so this must be nil")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAssetSubscription_Success(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)

	const userID, subID = uint64(1), uint64(10)

	mock.ExpectQuery(`SELECT user_id FROM asset_subscriptions`).
		WithArgs(subID).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(userID))
	mock.ExpectExec(`UPDATE asset_subscriptions SET`).
		WithArgs(false, subID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.UpdateAssetSubscription(userID, subID, user.UpdateAssetSubRequest{IsActive: boolPtr(false)})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
