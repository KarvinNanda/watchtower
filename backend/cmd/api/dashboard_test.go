package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/karvin-nanda/watchtower/internal/user"
)

func TestCountActiveSymbols(t *testing.T) {
	rules := []user.AssetRule{
		{AssetSymbol: "BTC", IsActive: true},
		{AssetSymbol: "BTC", IsActive: true}, // second rule on the same asset
		{AssetSymbol: "ETH", IsActive: true},
		{AssetSymbol: "AAPL", IsActive: false}, // paused: not tracked
	}
	assert.Equal(t, 2, countActiveSymbols(rules), "Total Assets Tracked counts assets, not rules")
}
