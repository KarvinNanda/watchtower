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
	registerAssetRuleRoutes(r.Group("/"), user.NewUserService(db, 100), 6)
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
	mock.ExpectExec(`INSERT INTO asset_rules \(user_id, asset_type, asset_symbol, rule_type, direction, intent, level_usd, pct, level_currency, level_idr\)`).
		WithArgs(7, "crypto", "BTC", "trailing_stop", nil, nil, nil, 10.0, "USD", nil).
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
		[]string{"asset_type", "asset_symbol", "rule_type", "direction", "intent", "level_usd", "pct", "is_active", "fired", "level_currency", "level_idr"}).
		AddRow("crypto", "BTC", "price_level", "above", nil, 100.0, nil, true, false, "USD", nil))
	mock.ExpectRollback()

	w := do(r, http.MethodPost, "/rules/assets/5/rearm", "")
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "conflict", errorCode(t, w))
}

func TestListRules_ExplicitNulls(t *testing.T) {
	r, mock := newRuleTestRouter(t)
	mock.ExpectQuery(`FROM asset_rules`).WillReturnRows(sqlmock.NewRows([]string{
		"id", "asset_type", "asset_symbol", "rule_type", "direction", "intent", "level_usd", "pct", "is_active",
		"peak_price_usd", "triggered_at", "triggered_price_usd", "last_alerted_at", "created_at", "level_currency", "level_idr", "week52_ref_usd", "peak_price_idr", "triggered_price_idr",
	}).AddRow(int64(1), "crypto", "BTC", "pct_change_24h", nil, nil, nil, 5.0, true, nil, nil, nil, nil, time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC), "USD", nil, nil, nil, nil))

	w := do(r, http.MethodGet, "/rules/assets", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"direction":null`)
	assert.Contains(t, w.Body.String(), `"peak_price_usd":null`)
	assert.Contains(t, w.Body.String(), `"level_idr":null`)
	assert.Contains(t, w.Body.String(), `"week52_ref_usd":null`)
	assert.Contains(t, w.Body.String(), `"peak_price_idr":null`)
	assert.Contains(t, w.Body.String(), `"triggered_price_idr":null`)
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

func TestListRules_CarriesSchedulerInterval(t *testing.T) {
	r, mock := newRuleTestRouter(t)
	mock.ExpectQuery(`FROM asset_rules`).WillReturnRows(sqlmock.NewRows([]string{
		"id", "asset_type", "asset_symbol", "rule_type", "direction", "intent", "level_usd", "pct", "is_active",
		"peak_price_usd", "triggered_at", "triggered_price_usd", "last_alerted_at", "created_at",
	}))

	w := do(r, http.MethodGet, "/rules/assets", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"meta":{"asset_interval_hours":6}`,
		"the UI help text shows the configured interval, not a hard-coded 4 hours")
}

func TestCreateRule_IDRLevelIsStoredAsIDR(t *testing.T) {
	r, mock := newRuleTestRouter(t)
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(true))
	mock.ExpectExec(`INSERT INTO asset_rules`).
		WithArgs(7, "gold", "ANTAM", "price_level", "below", "buy_target", nil, nil, "IDR", 1350000.0).
		WillReturnResult(sqlmock.NewResult(1, 1))

	w := do(r, http.MethodPost, "/rules/assets",
		`{"asset_type":"gold","asset_symbol":"ANTAM","rule_type":"price_level","intent":"buy_target","level_idr":1350000,"level_currency":"USD"}`)

	assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRule_BothLevelsIs400(t *testing.T) {
	r, _ := newRuleTestRouter(t)
	w := do(r, http.MethodPost, "/rules/assets",
		`{"asset_type":"crypto","asset_symbol":"BTC","rule_type":"price_level","direction":"above","level_usd":1,"level_idr":1}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "validation_error", errorCode(t, w))
}

func TestAssetRuleResponse_HasCurrencyFields(t *testing.T) {
	idr := 1350000.0
	got := toAssetRuleResponse(user.AssetRule{RuleType: "price_level", LevelCurrency: "IDR", LevelIDR: &idr})
	body, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"level_currency":"IDR"`)
	assert.Contains(t, string(body), `"level_idr":1350000`)
}

func TestAssetRuleResponse_HasWeek52Ref(t *testing.T) {
	ref := 126080.0
	body, err := json.Marshal(toAssetRuleResponse(user.AssetRule{RuleType: "week52_break", Week52RefUSD: &ref}))
	require.NoError(t, err)
	assert.Contains(t, string(body), `"week52_ref_usd":126080`)
}
