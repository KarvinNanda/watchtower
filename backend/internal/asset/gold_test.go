package asset

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/currency"
)

func TestGoldResult_ConvertsWithARealRate(t *testing.T) {
	now := time.Date(2026, 10, 15, 2, 0, 0, 0, time.UTC)
	res, err := goldResult(2392157, currency.Rate{Value: 17908}, now)
	require.NoError(t, err)
	assert.InDelta(t, 2392157.0/17908, res.PriceUSD, 1e-9)
	assert.Equal(t, 2392157.0, res.PriceIDR)
	assert.Equal(t, "ANTAM", res.Symbol)
	assert.Equal(t, now, res.FetchedAt)
}

func TestGoldResult_RefusesTheFallbackRate(t *testing.T) {
	// ANTAM rules are evaluated on PriceUSD; 2392157/16000 would be ~12%
	// too high and could fire or miss a level.
	_, err := goldResult(2392157, currency.Rate{Value: 16000, Fallback: true}, time.Now())
	assert.Error(t, err)
}

func TestGoldResult_RefusesAZeroRate(t *testing.T) {
	_, err := goldResult(2392157, currency.Rate{}, time.Now())
	assert.Error(t, err)
}
