package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/user"
)

// #41: an unknown IDR price (stored as 0) is sent as null, so the market
// card shows "—" instead of "Rp 0".
func TestMarketDataResponse_UnknownIDRIsNull(t *testing.T) {
	b, err := json.Marshal(toMarketDataResponse(user.MarketData{Symbol: "BTC", PriceUSD: 150}))
	require.NoError(t, err)
	assert.Contains(t, string(b), `"price_idr":null`)

	b, err = json.Marshal(toMarketDataResponse(user.MarketData{Symbol: "BTC", PriceUSD: 150, PriceIDR: 2686200}))
	require.NoError(t, err)
	assert.Contains(t, string(b), `"price_idr":2686200`)
}

func TestIDROrNil(t *testing.T) {
	assert.Nil(t, idrOrNil(0))
	require.NotNil(t, idrOrNil(1.5))
	assert.Equal(t, 1.5, *idrOrNil(1.5))
}
