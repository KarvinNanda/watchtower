package runs

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() { gin.SetMode(gin.TestMode) }

type fakeLinks struct{ linked bool }

func (f fakeLinks) TelegramLinked(context.Context, uint64, string) (bool, error) { return f.linked, nil }

type testAPI struct {
	router *gin.Engine
	store  *Store
	mr     *miniredis.Miniredis
	userID uint64
}

func newTestAPI(t *testing.T, linked bool) *testAPI {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	api := &testAPI{store: NewStore(rdb), mr: mr, userID: 7}

	r := gin.New()
	g := r.Group("/api", func(c *gin.Context) {
		c.Set("user_id", api.userID) // what auth.AuthMiddleware sets
		c.Next()
	})
	RegisterRoutes(g, api.store, fakeLinks{linked: linked})
	api.router = r
	return api
}

func (a *testAPI) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.router.ServeHTTP(w, req)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	return m
}

func TestCreateRun_InvalidType(t *testing.T) {
	api := newTestAPI(t, true)
	w := api.do(http.MethodPost, "/api/runs", `{"type":"portfolio"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "validation_error", decode(t, w)["error"])
}

func TestCreateRun_SchedulerOffline(t *testing.T) {
	api := newTestAPI(t, true)
	w := api.do(http.MethodPost, "/api/runs", `{"type":"asset"}`)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "scheduler_offline", decode(t, w)["error"])
}

func TestCreateRun_TelegramNotLinked(t *testing.T) {
	api := newTestAPI(t, false)
	require.NoError(t, api.store.Heartbeat(context.Background()))
	w := api.do(http.MethodPost, "/api/runs", `{"type":"sentinel"}`)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "telegram_not_linked", decode(t, w)["error"])
}

func TestCreateRun_SecondCallReturns429(t *testing.T) {
	api := newTestAPI(t, true)
	require.NoError(t, api.store.Heartbeat(context.Background()))

	first := api.do(http.MethodPost, "/api/runs", `{"type":"asset"}`)
	require.Equal(t, http.StatusAccepted, first.Code)
	data := decode(t, first)["data"].(map[string]interface{})
	assert.Equal(t, "queued", data["status"])
	assert.Regexp(t, `^[0-9a-f]{32}$`, data["run_id"])
	assert.NotContains(t, data, "user_id")

	second := api.do(http.MethodPost, "/api/runs", `{"type":"asset"}`)
	assert.Equal(t, http.StatusTooManyRequests, second.Code)
	body := decode(t, second)
	assert.Equal(t, "too_many_runs", body["error"])
	assert.Equal(t, float64(300), body["retry_after"])
	assert.Equal(t, "300", second.Header().Get("Retry-After"))
}

func TestGetRun_OwnerSeesRun(t *testing.T) {
	api := newTestAPI(t, true)
	run, err := api.store.Enqueue(context.Background(), 7, TypeAsset)
	require.NoError(t, err)

	w := api.do(http.MethodGet, "/api/runs/"+run.ID, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, run.ID, decode(t, w)["data"].(map[string]interface{})["run_id"])
}

func TestGetRun_OtherUserGets404(t *testing.T) {
	api := newTestAPI(t, true)
	run, err := api.store.Enqueue(context.Background(), 99, TypeAsset)
	require.NoError(t, err)

	w := api.do(http.MethodGet, "/api/runs/"+run.ID, "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetRun_MalformedIDGets404(t *testing.T) {
	api := newTestAPI(t, true)
	w := api.do(http.MethodGet, "/api/runs/not-a-run-id", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSQLTelegramLinkChecker(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	c := SQLTelegramLinkChecker{DB: db}

	mock.ExpectQuery(`SELECT telegram_asset_chat_id IS NOT NULL FROM users WHERE id = \?`).
		WithArgs(7).WillReturnRows(sqlmock.NewRows([]string{"linked"}).AddRow(1))
	linked, err := c.TelegramLinked(context.Background(), 7, TypeAsset)
	require.NoError(t, err)
	assert.True(t, linked)

	mock.ExpectQuery(`SELECT telegram_sentinel_chat_id IS NOT NULL FROM users WHERE id = \?`).
		WithArgs(7).WillReturnRows(sqlmock.NewRows([]string{"linked"}).AddRow(0))
	linked, err = c.TelegramLinked(context.Background(), 7, TypeSentinel)
	require.NoError(t, err)
	assert.False(t, linked)

	mock.ExpectQuery(`FROM users WHERE id = \?`).
		WithArgs(8).WillReturnRows(sqlmock.NewRows([]string{"linked"}))
	linked, err = c.TelegramLinked(context.Background(), 8, TypeAsset)
	require.NoError(t, err)
	assert.False(t, linked, "unknown user is not linked")

	assert.NoError(t, mock.ExpectationsWereMet())
}
