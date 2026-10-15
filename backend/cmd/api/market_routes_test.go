package main

import (
	"net/http"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/db"
	"github.com/karvin-nanda/watchtower/internal/user"
)

func newMarketTestRouter(t *testing.T, setUser bool) (*gin.Engine, sqlmock.Sqlmock) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	r := gin.New()
	if setUser {
		r.Use(func(c *gin.Context) { c.Set("user_id", uint64(7)); c.Next() })
	}
	registerMarketRoutes(r.Group("/"), user.NewUserService(sqlDB, 100))
	return r, mock
}

func TestMarketSnapshot_QueriesWithCallerID(t *testing.T) {
	r, mock := newMarketTestRouter(t, true)
	mock.ExpectQuery(`SELECT mc\.symbol`).WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"symbol", "price_usd", "price_idr", "change_pct_24h", "last_fetched", "source"}).
			AddRow("BTC", 65000.0, 0.0, nil, time.Now(), "coingecko"))

	w := do(r, http.MethodGet, "/market/snapshot", "")

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMarketSnapshot_WithoutUserIs401(t *testing.T) {
	r, mock := newMarketTestRouter(t, false)

	w := do(r, http.MethodGet, "/market/snapshot", "")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet(), "no query may run without a user")
}

// The per-symbol endpoint answered 200/404 depending on whether anyone on
// the instance tracked the symbol (#53). It must not come back.
func TestRegisterRoutes_NoPerSymbolMarketRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerRoutes(r, &db.DB{}, nil, nil, false, 6)

	paths := map[string]bool{}
	for _, rt := range r.Routes() {
		paths[rt.Method+" "+rt.Path] = true
	}
	assert.True(t, paths["GET /api/market/snapshot"])
	assert.False(t, paths["GET /api/market/:symbol"])
}

// The dashboard embeds the market snapshot; it must use the caller's id too,
// or the #53 leak comes back through /api/dashboard.
func TestDashboard_SnapshotUsesCallerID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", uint64(7)); c.Next() })
	r.GET("/dashboard", dashboardHandler(user.NewUserService(sqlDB, 100)))

	empty := func(cols ...string) *sqlmock.Rows { return sqlmock.NewRows(cols) }
	mock.ExpectQuery(`FROM asset_rules`).WillReturnRows(empty("id"))
	mock.ExpectQuery(`.`).WillReturnRows(empty("id")) // keyword subscriptions
	mock.ExpectQuery(`MAX\(sent_at\)`).WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(nil))
	mock.ExpectQuery(`MAX\(sent_at\)`).WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(nil))
	mock.ExpectQuery(`FROM market_cache mc`).WithArgs(uint64(7)).
		WillReturnRows(empty("symbol", "price_usd", "price_idr", "change_pct_24h", "last_fetched", "source"))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM notification_logs`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`FROM notification_logs`).WillReturnRows(empty("id"))

	w := do(r, http.MethodGet, "/dashboard", "")

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NoError(t, mock.ExpectationsWereMet())
}
