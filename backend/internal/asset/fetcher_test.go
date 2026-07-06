package asset

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// pricesFromDeltas returns a cumulative-sum price series starting at start,
// applying each delta in order — a convenient way to build a closes slice
// with an exactly-known set of day-over-day deltas for RSI/trend/signal
// assertions, rather than hand-computing absolute prices.
func pricesFromDeltas(start float64, deltas ...float64) []float64 {
	closes := make([]float64, 0, len(deltas)+1)
	closes = append(closes, start)
	price := start
	for _, d := range deltas {
		price += d
		closes = append(closes, price)
	}
	return closes
}

func TestCalculateRSI_Oversold(t *testing.T) {
	t.Parallel()

	// 14 closes, consistently falling -> every delta is a loss -> RSI 0.
	closes := pricesFromDeltas(128, -2, -2, -2, -2, -2, -2, -2, -2, -2, -2, -2, -2, -2)
	require14 := len(closes)
	if require14 != 14 {
		t.Fatalf("test setup error: expected 14 closes, got %d", require14)
	}

	rsi := computeRSI(closes)
	assert.Less(t, rsi, 35.0, "RSI should be oversold (< 35) for a consistent downtrend")
	assert.Equal(t, "BUY", computeSignal(rsi))
}

func TestCalculateRSI_Overbought(t *testing.T) {
	t.Parallel()

	// 14 closes, consistently rising -> every delta is a gain -> RSI 100.
	closes := pricesFromDeltas(100, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2)

	rsi := computeRSI(closes)
	assert.Greater(t, rsi, 65.0, "RSI should be overbought (> 65) for a consistent uptrend")
	assert.Equal(t, "SELL", computeSignal(rsi))
}

func TestCalculateRSI_Neutral(t *testing.T) {
	t.Parallel()

	// 14 closes oscillating with roughly balanced gains/losses -> RSI
	// should land in the neutral 35-65 band.
	closes := pricesFromDeltas(100, 2, -2, 2, -2, 2, -2, 2, -2, 2, -2, 2, -2, 2)

	rsi := computeRSI(closes)
	assert.GreaterOrEqual(t, rsi, 35.0)
	assert.LessOrEqual(t, rsi, 65.0)
	assert.Equal(t, "HOLD", computeSignal(rsi))
}

func TestCalculateRSI_InsufficientData(t *testing.T) {
	t.Parallel()

	// Fewer than technicalIndicatorWindow (14) closes -> defined as
	// "insufficient" and defaults to a neutral RSI of 50 rather than
	// computing a misleadingly extreme value off a handful of deltas.
	closes := []float64{100, 101, 99, 102}

	rsi := computeRSI(closes)
	assert.Equal(t, 50.0, rsi)
	assert.Equal(t, "HOLD", computeSignal(rsi))
}

func TestCalculateVolatility(t *testing.T) {
	t.Parallel()

	// 15 closes (14 returns) alternating +2%/-2% around a base of 100 ->
	// mean return is exactly 0, so the population standard deviation of
	// returns is exactly 0.02. Annualized: 0.02 * sqrt(252) * 100 ≈ 31.749%.
	deltas := make([]float64, 14)
	price := 100.0
	closes := make([]float64, 0, 15)
	closes = append(closes, price)
	for i := 0; i < 14; i++ {
		if i%2 == 0 {
			deltas[i] = price * 0.02
		} else {
			deltas[i] = -price * 0.02
		}
		price += deltas[i]
		closes = append(closes, price)
	}

	vol := computeAnnualizedVolatility(closes)
	assert.InDelta(t, 31.749, vol, 0.1)
}

func TestDetermineTrend_Bullish(t *testing.T) {
	t.Parallel()

	// Current price more than 1% above the SMA -> BULLISH.
	trend := computeTrend(115, 100)
	assert.Equal(t, "BULLISH", trend)
}

func TestDetermineTrend_Bearish(t *testing.T) {
	t.Parallel()

	// Current price more than 1% below the SMA -> BEARISH.
	trend := computeTrend(85, 100)
	assert.Equal(t, "BEARISH", trend)
}

func TestDetermineTrend_Neutral(t *testing.T) {
	t.Parallel()

	// Current price within 1% of the SMA -> NEUTRAL.
	trend := computeTrend(100.5, 100)
	assert.Equal(t, "NEUTRAL", trend)
}
