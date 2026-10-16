package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/karvin-nanda/watchtower/internal/auth"
)

func newPasswordRouter(t *testing.T) (*gin.Engine, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	svc := auth.NewService(db, "test-secret", 1)
	r := gin.New()
	r.PUT("/api/auth/password", func(c *gin.Context) { c.Set("user_id", uint64(9)); c.Next() },
		changePasswordHandler(svc, false))
	return r, mock
}

func expectUser9(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("Old1Pass!"), bcrypt.MinCost)
	require.NoError(t, err)
	mock.ExpectQuery(`FROM users WHERE id = \?`).WithArgs(uint64(9)).WillReturnRows(sqlmock.NewRows([]string{
		"id", "email", "password_hash", "token_version", "telegram_asset_chat_id", "telegram_sentinel_chat_id",
		"alert_cooldown_hours", "preferred_language", "is_active", "created_at", "updated_at",
	}).AddRow(uint64(9), "me@example.com", string(hash), uint32(0), nil, nil, 4, "id", true, time.Now(), time.Now()))
}

func putPassword(r *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/api/auth/password", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestChangePassword_OKSetsANewCookie(t *testing.T) {
	r, mock := newPasswordRouter(t)
	expectUser9(t, mock)
	mock.ExpectExec(`UPDATE users SET password_hash = \?, token_version = token_version \+ 1`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := putPassword(r, `{"current_password":"Old1Pass!","new_password":"New2Pass@"}`)

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Header().Get("Set-Cookie"), "watchtower_token=")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChangePassword_WrongCurrentIs400(t *testing.T) {
	r, mock := newPasswordRouter(t)
	expectUser9(t, mock)

	w := putPassword(r, `{"current_password":"Nope1Pass!","new_password":"New2Pass@"}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "wrong_password", errorCode(t, w))
	assert.Empty(t, w.Header().Get("Set-Cookie"))
}

func TestChangePassword_WeakOrSameIsValidationError(t *testing.T) {
	for _, next := range []string{"weakpassword", "Old1Pass!"} {
		r, mock := newPasswordRouter(t)
		expectUser9(t, mock)

		w := putPassword(r, `{"current_password":"Old1Pass!","new_password":"`+next+`"}`)

		assert.Equal(t, http.StatusBadRequest, w.Code, next)
		assert.Equal(t, "validation_error", errorCode(t, w), next)
	}
}

func TestChangePassword_MissingFieldsIs400(t *testing.T) {
	r, _ := newPasswordRouter(t)
	w := putPassword(r, `{"new_password":"New2Pass@"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "validation_error", errorCode(t, w))
}
