package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/asset"
)

type ctxKey struct{}

// #86: the refetch runs under the caller's context, so the caller's fetch
// deadline bounds the external call.
func TestGetMarketData_FetchUsesCallerContext(t *testing.T) {
	f := &fakeFetcher{res: &asset.FetchResult{PriceUSD: 200, FetchedAt: now}}
	c, mock := freshnessCache(t, nil, f, &fakeBudget{})
	mock.ExpectQuery(`FROM market_cache`).WillReturnRows(mock.NewRows(marketColumns))
	expectUpsert(mock)
	ctx := context.WithValue(context.Background(), ctxKey{}, "caller")

	_, err := c.GetMarketData(ctx, "BTC", "crypto", FetchOptions{})

	require.NoError(t, err)
	require.NotNil(t, f.assetCtx)
	assert.Equal(t, "caller", f.assetCtx.Value(ctxKey{}))
}

// #86: the daily closes fetch runs under stockChange7d's own 20 s context,
// not the caller's, so an expired caller context does not skip it and the
// own deadline bounds the HTTP call.
func TestStockChange7d_ClosesFetchUsesItsOwnContext(t *testing.T) {
	f := twoCloses()
	c, _ := freshnessCache(t, nil, f, &fakeBudget{})
	caller, cancel := context.WithCancel(context.Background())
	cancel()

	got := c.stockChange7d(caller, "AAPL", 103.5, session, false)

	require.NotNil(t, got)
	require.NotNil(t, f.closesCtx)
	assert.NoError(t, f.closesCtxErr, "the caller's cancel must not reach the closes fetch")
	deadline, ok := f.closesCtx.Deadline()
	require.True(t, ok, "the closes fetch must have a deadline")
	assert.WithinDuration(t, time.Now().Add(closes7dTimeout), deadline, 2*time.Second)
}
