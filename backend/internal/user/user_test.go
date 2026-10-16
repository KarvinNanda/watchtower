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

func TestGetMarketSnapshot_OnlyCallersSymbols(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)

	rows := sqlmock.NewRows([]string{"symbol", "price_usd", "price_idr", "change_pct_24h", "last_fetched", "source"}).
		AddRow("BTC", 65000.0, 1040000000.0, nil, time.Now(), "coingecko")
	// Owner filter, and no is_active filter: a paused rule's card still shows
	// the last known price (#53, Spec 4a).
	mock.ExpectQuery(`FROM market_cache mc\s+WHERE mc\.symbol IN \(\s+SELECT DISTINCT asset_symbol FROM asset_rules WHERE user_id = \?\s+\)`).
		WithArgs(uint64(1)).
		WillReturnRows(rows)

	data, err := svc.GetMarketSnapshot(1)

	require.NoError(t, err)
	require.Len(t, data, 1)
	assert.Equal(t, "BTC", data[0].Symbol)
	assert.Nil(t, data[0].ChangePct24h)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetMarketSnapshot_NoRulesIsEmptySlice(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectQuery(`FROM market_cache mc`).WithArgs(uint64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"symbol", "price_usd", "price_idr", "change_pct_24h", "last_fetched", "source"}))

	data, err := svc.GetMarketSnapshot(2)

	require.NoError(t, err)
	assert.NotNil(t, data, "JSON must be [] not null")
	assert.Empty(t, data)
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
		WithArgs(uint64(1), "status report:%").
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

// #7: a status report is a sent asset message but not an alert, so it must
// not move the dashboard's "Last Asset Alert".
func TestGetLastAlertTimestamps_IgnoresStatusReports(t *testing.T) {
	t.Parallel()
	svc, mock := newMockUserService(t, 100)
	mock.ExpectQuery(`notif_type = 'asset' AND status = 'sent' AND content_summary NOT LIKE \?`).
		WithArgs(uint64(1), "status report:%").
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(nil))
	mock.ExpectQuery(`notif_type = 'sentinel'`).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(nil))

	lastAsset, _, err := svc.GetLastAlertTimestamps(1)

	require.NoError(t, err)
	assert.Nil(t, lastAsset)
	require.NoError(t, mock.ExpectationsWereMet())
}
