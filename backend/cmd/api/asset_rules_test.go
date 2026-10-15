package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/user"
)

func newRuleTestRouter(t *testing.T) (*gin.Engine, sqlmock.Sqlmock) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", uint64(7)); c.Next() })
	registerAssetRuleRoutes(r.Group("/"), user.NewUserService(db, 100))
	return r, mock
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	var body struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body.Error
}

func TestCreateRule_IgnoresStateFieldsInBody(t *testing.T) {
	r, mock := newRuleTestRouter(t)
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(true))
	mock.ExpectExec(`INSERT INTO asset_rules \(user_id, asset_type, asset_symbol, rule_type, direction, intent, level_usd, pct\)`).
		WithArgs(7, "crypto", "BTC", "trailing_stop", nil, nil, nil, 10.0).
		WillReturnResult(sqlmock.NewResult(1, 1))

	w := do(r, http.MethodPost, "/rules/assets",
		`{"asset_type":"crypto","asset_symbol":"BTC","rule_type":"trailing_stop","pct":10,"peak_price_usd":1,"baseline_pending":true,"params_version":99}`)

	assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRule_ConflictingIntentIs400(t *testing.T) {
	r, _ := newRuleTestRouter(t)
	w := do(r, http.MethodPost, "/rules/assets",
		`{"asset_type":"crypto","asset_symbol":"BTC","rule_type":"price_level","intent":"take_profit","direction":"below","level_usd":1}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "validation_error", errorCode(t, w))
}

func TestUpdateRule_OtherUsersRuleIs404(t *testing.T) {
	r, mock := newRuleTestRouter(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`FOR UPDATE`).WithArgs(99, 7).WillReturnRows(sqlmock.NewRows([]string{"asset_type"}))
	mock.ExpectRollback()

	w := do(r, http.MethodPut, "/rules/assets/99", `{"is_active":false}`)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "not_found", errorCode(t, w))
}

func TestUpdateRule_PartialDefinitionIs400(t *testing.T) {
	r, _ := newRuleTestRouter(t)
	w := do(r, http.MethodPut, "/rules/assets/5", `{"level_usd":120000}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDeleteRule_NotFoundIs404(t *testing.T) {
	r, mock := newRuleTestRouter(t)
	mock.ExpectExec(`DELETE FROM asset_rules`).WithArgs(99, 7).WillReturnResult(sqlmock.NewResult(0, 0))
	w := do(r, http.MethodDelete, "/rules/assets/99", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRearmRule_NonTrailingIs409(t *testing.T) {
	r, mock := newRuleTestRouter(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`FOR UPDATE`).WillReturnRows(sqlmock.NewRows(
		[]string{"asset_type", "asset_symbol", "rule_type", "direction", "intent", "level_usd", "pct", "is_active", "fired"}).
		AddRow("crypto", "BTC", "price_level", "above", nil, 100.0, nil, true, false))
	mock.ExpectRollback()

	w := do(r, http.MethodPost, "/rules/assets/5/rearm", "")
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "conflict", errorCode(t, w))
}

func TestListRules_ExplicitNulls(t *testing.T) {
	r, mock := newRuleTestRouter(t)
	mock.ExpectQuery(`FROM asset_rules`).WillReturnRows(sqlmock.NewRows([]string{
		"id", "asset_type", "asset_symbol", "rule_type", "direction", "intent", "level_usd", "pct", "is_active",
		"peak_price_usd", "triggered_at", "triggered_price_usd", "last_alerted_at", "created_at",
	}).AddRow(int64(1), "crypto", "BTC", "pct_change_24h", nil, nil, nil, 5.0, true, nil, nil, nil, nil, time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)))

	w := do(r, http.MethodGet, "/rules/assets", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"direction":null`)
	assert.Contains(t, w.Body.String(), `"peak_price_usd":null`)
}

func TestCreateRule_SymbolOfAnotherTypeIs400WithClearMessage(t *testing.T) {
	r, _ := newRuleTestRouter(t)
	w := do(r, http.MethodPost, "/rules/assets", `{"asset_type":"stock","asset_symbol":"BTC","rule_type":"pct_change_24h","pct":5}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "BTC is not a stock symbol")
}

func TestUpdateRule_EmptyBodyIs400(t *testing.T) {
	r, mock := newRuleTestRouter(t)
	w := do(r, http.MethodPut, "/rules/assets/424242", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "an update that changes nothing must not answer 200 for any id")
	assert.Equal(t, "validation_error", errorCode(t, w))
	assert.NoError(t, mock.ExpectationsWereMet())
}
