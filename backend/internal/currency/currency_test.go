package currency

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useServer points the package at a test server and clears the cache. The
// package keeps global state, so these tests must not run in parallel.
func useServer(t *testing.T, status int, body []byte) *atomic.Int32 {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	oldURL := BaseURL
	BaseURL = srv.URL
	setCache(nil)
	t.Cleanup(func() { BaseURL = oldURL; setCache(nil) })
	return &hits
}

func setCache(r *ExchangeRate) {
	rateMu.Lock()
	cachedRate = r
	rateMu.Unlock()
}

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/open_er_usd.json")
	require.NoError(t, err)
	return b
}

func TestCurrent_ReadsRateAndSourceUpdateTime(t *testing.T) {
	useServer(t, http.StatusOK, fixture(t))
	r := Current()
	assert.InDelta(t, 17907.815096, r.Value, 1e-9)
	assert.Equal(t, time.Unix(1792022551, 0).UTC(), r.UpdatedAt.UTC())
	assert.False(t, r.Fallback)
}

func TestCurrent_ServesFreshCacheWithoutRequest(t *testing.T) {
	hits := useServer(t, http.StatusOK, fixture(t))
	first := Current()
	second := Current()
	assert.Equal(t, first, second)
	assert.Equal(t, int32(1), hits.Load())
}

func TestCurrent_StaleCacheKeepsItsOwnUpdateTime(t *testing.T) {
	useServer(t, http.StatusInternalServerError, nil)
	updated := time.Date(2026, 10, 14, 0, 2, 0, 0, time.UTC)
	setCache(&ExchangeRate{Rate: 17800, UpdatedAt: updated, FetchedAt: time.Now().Add(-2 * time.Hour)})
	r := Current()
	assert.Equal(t, Rate{Value: 17800, UpdatedAt: updated}, r)
}

func TestCurrent_NothingCachedIsFlaggedFallback(t *testing.T) {
	useServer(t, http.StatusInternalServerError, nil)
	r := Current()
	assert.True(t, r.Fallback)
	assert.Equal(t, 16000.0, r.Value)
	assert.True(t, r.UpdatedAt.IsZero())
}

func TestCurrent_MissingUpdateTimeIsZero(t *testing.T) {
	useServer(t, http.StatusOK, []byte(`{"result":"success","rates":{"IDR":17900}}`))
	r := Current()
	assert.Equal(t, 17900.0, r.Value)
	assert.True(t, r.UpdatedAt.IsZero())
}

func TestGetUSDToIDR_StillReturnsTheValue(t *testing.T) {
	useServer(t, http.StatusOK, fixture(t))
	v, err := GetUSDToIDR()
	require.NoError(t, err)
	assert.InDelta(t, 17907.815096, v, 1e-9)
}
