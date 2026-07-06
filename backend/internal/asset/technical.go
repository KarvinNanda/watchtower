package asset

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
)

const (
	twelveDataTimeSeriesURL = "https://api.twelvedata.com/time_series"

	// technicalIndicatorWindow is the number of most-recent daily closes
	// used for the SMA/range/target-band computations below. One extra
	// close (technicalIndicatorWindow+1 requested from Twelve Data) is
	// fetched so there are exactly technicalIndicatorWindow day-over-day
	// deltas available for a true 14-period RSI/volatility, rather than the
	// 13 deltas a naive "just fetch 14 closes" reading would produce.
	technicalIndicatorWindow = 14

	tradingDaysPerYear = 252
)

// enrichWithTechnicalIndicators best-effort fetches recent daily closes for
// symbol from Twelve Data and populates result's RSI/Volatility/Trend/
// Signal/range/target fields. Failures are logged and swallowed rather than
// returned to the caller — the current price quote (already in result) is
// the time-sensitive data subscribers are alerted on, and is worth
// delivering even if the supplementary technical commentary can't be
// computed this run (e.g. Twelve Data's free tier daily quota is
// exhausted).
//
// Only stock symbols get technical indicators here: Twelve Data's
// /time_series endpoint is a stock/forex history feed with no crypto or
// Antam gold equivalent wired up in this codebase, so FetchCrypto/FetchGold
// results are left with these fields at their zero value.
func (f *AssetFetcher) enrichWithTechnicalIndicators(result *FetchResult, symbol string) {
	closes, err := f.fetchDailyCloses(symbol, technicalIndicatorWindow+1)
	if err != nil {
		log.Printf("[WARN] enrichWithTechnicalIndicators: fetch daily closes for %s: %v", symbol, err)
		return
	}
	if len(closes) < 2 {
		log.Printf("[WARN] enrichWithTechnicalIndicators: not enough history for %s (%d closes)", symbol, len(closes))
		return
	}

	window := closes
	if len(window) > technicalIndicatorWindow {
		window = window[len(window)-technicalIndicatorWindow:]
	}
	sma := mean(window)
	stddev := stddevOf(window, sma)
	rangeLow, rangeHigh := minMax(window)

	result.RSI = computeRSI(closes)
	result.Volatility = computeAnnualizedVolatility(closes)
	result.Trend = computeTrend(result.PriceUSD, sma)
	result.Signal = computeSignal(result.RSI)
	result.RangeLow14D = rangeLow
	result.RangeHigh14D = rangeHigh
	result.TargetBuyUSD = sma - stddev
	result.TargetSellUSD = sma + stddev
}

// fetchDailyCloses returns the last outputsize daily closing prices for
// symbol from Twelve Data's /time_series endpoint, oldest first.
func (f *AssetFetcher) fetchDailyCloses(symbol string, outputsize int) ([]float64, error) {
	recordStockAPICall()

	endpoint := fmt.Sprintf("%s?symbol=%s&interval=1day&outputsize=%d&apikey=%s",
		twelveDataTimeSeriesURL, url.QueryEscape(symbol), outputsize, url.QueryEscape(f.twelveDataKey))

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build time_series request for %s: %w", symbol, err)
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch time_series for %s: %w", symbol, err)
	}
	defer resp.Body.Close()

	var raw struct {
		Values []struct {
			Close string `json:"close"`
		} `json:"values"`
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode time_series response for %s: %w", symbol, err)
	}
	if raw.Status == "error" {
		return nil, fmt.Errorf("twelve data time_series error for %s: %s", symbol, raw.Message)
	}
	if len(raw.Values) == 0 {
		return nil, fmt.Errorf("twelve data returned no time_series values for %s", symbol)
	}

	// Twelve Data returns values newest-first; reverse to oldest-first so
	// delta/return calculations below read chronologically forward.
	closes := make([]float64, 0, len(raw.Values))
	for i := len(raw.Values) - 1; i >= 0; i-- {
		c, err := strconv.ParseFloat(raw.Values[i].Close, 64)
		if err != nil {
			continue
		}
		closes = append(closes, c)
	}
	return closes, nil
}

// computeRSI implements the standard RSI over every day-over-day delta in
// closes (chronological order).
func computeRSI(closes []float64) float64 {
	if len(closes) < 2 {
		return 0
	}

	var gainSum, lossSum float64
	for i := 1; i < len(closes); i++ {
		delta := closes[i] - closes[i-1]
		if delta > 0 {
			gainSum += delta
		} else {
			lossSum += -delta
		}
	}

	count := float64(len(closes) - 1)
	avgGain := gainSum / count
	avgLoss := lossSum / count
	if avgLoss == 0 {
		return 100
	}

	rs := avgGain / avgLoss
	return 100 - (100 / (1 + rs))
}

// computeAnnualizedVolatility returns the annualized standard deviation (as
// a percentage) of the daily returns implied by closes.
func computeAnnualizedVolatility(closes []float64) float64 {
	if len(closes) < 2 {
		return 0
	}

	returns := make([]float64, 0, len(closes)-1)
	for i := 1; i < len(closes); i++ {
		if closes[i-1] == 0 {
			continue
		}
		returns = append(returns, (closes[i]-closes[i-1])/closes[i-1])
	}
	if len(returns) == 0 {
		return 0
	}

	m := mean(returns)
	dailyStdDev := stddevOf(returns, m)
	return dailyStdDev * math.Sqrt(tradingDaysPerYear) * 100
}

// computeTrend compares currentPrice against sma (the simple moving average
// over the technical indicator window): BULLISH if more than 1% above,
// BEARISH if more than 1% below, NEUTRAL within that band.
func computeTrend(currentPrice, sma float64) string {
	if sma == 0 {
		return "NEUTRAL"
	}
	deltaPct := (currentPrice - sma) / sma * 100
	switch {
	case deltaPct > 1:
		return "BULLISH"
	case deltaPct < -1:
		return "BEARISH"
	default:
		return "NEUTRAL"
	}
}

// computeSignal derives a coarse BUY/SELL/HOLD signal from RSI alone.
func computeSignal(rsi float64) string {
	switch {
	case rsi < 35:
		return "BUY"
	case rsi > 65:
		return "SELL"
	default:
		return "HOLD"
	}
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func stddevOf(values []float64, m float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sumSq float64
	for _, v := range values {
		d := v - m
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(values)))
}

func minMax(values []float64) (min, max float64) {
	if len(values) == 0 {
		return 0, 0
	}
	min, max = values[0], values[0]
	for _, v := range values[1:] {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	return min, max
}
