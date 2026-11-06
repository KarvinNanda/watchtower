package asset

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// twelveDataTimeSeriesURL is a variable so tests can point it at a stub.
var twelveDataTimeSeriesURL = "https://api.twelvedata.com/time_series"

// DailyClose is one trading day's closing price. Day is the trading date at
// 00:00 UTC.
type DailyClose struct {
	Day   time.Time
	Close float64
}

// FetchStockRef7d returns the close from about 7 days ago for a stock: the
// latest trading day on or before today minus 7 days. It costs one Twelve
// Data credit.
func (f *AssetFetcher) FetchStockRef7d(symbol string) (DailyClose, error) {
	f.stockPacer.wait()
	recordStockAPICall()

	q := url.Values{}
	q.Set("symbol", symbol)
	q.Set("interval", "1day")
	q.Set("outputsize", "10")
	// The key goes in the Authorization header, never in the URL: a network
	// error quotes the full URL, and that error is logged (#74).
	req, err := http.NewRequest(http.MethodGet, twelveDataTimeSeriesURL+"?"+q.Encode(), nil)
	if err != nil {
		return DailyClose{}, fmt.Errorf("asset: build twelve data time series request for %s: %w", symbol, err)
	}
	req.Header.Set("Authorization", "apikey "+f.twelveDataKey)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return DailyClose{}, fmt.Errorf("asset: fetch stock time series %s: %w", symbol, err)
	}
	defer resp.Body.Close()

	closes, err := decodeTwelveDataTimeSeries(resp.Body, symbol)
	if err != nil {
		return DailyClose{}, err
	}
	ref, ok := pickRef7d(closes, time.Now())
	if !ok {
		return DailyClose{}, fmt.Errorf("asset: no 7-day reference close for %s", symbol)
	}
	return ref, nil
}

// decodeTwelveDataTimeSeries parses a /time_series response. Entries whose
// date or close cannot be read, or whose close is not above zero, are
// skipped. The order of the source (newest first) is kept.
func decodeTwelveDataTimeSeries(r io.Reader, symbol string) ([]DailyClose, error) {
	var raw struct {
		Values []struct {
			Datetime string `json:"datetime"`
			Close    string `json:"close"`
		} `json:"values"`
		Status  string `json:"status"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, fmt.Errorf("asset: decode twelve data time series for %s: %w", symbol, err)
	}
	if raw.Status == "error" {
		return nil, fmt.Errorf("asset: twelve data time series error for %s (code %d): %s", symbol, raw.Code, raw.Message)
	}

	closes := make([]DailyClose, 0, len(raw.Values))
	for _, v := range raw.Values {
		day, err := time.Parse("2006-01-02", v.Datetime)
		if err != nil {
			continue
		}
		c, err := strconv.ParseFloat(v.Close, 64)
		if err != nil || c <= 0 {
			continue
		}
		closes = append(closes, DailyClose{Day: day, Close: c})
	}
	return closes, nil
}

// pickRef7d returns the close with the latest Day that is on or before the
// UTC date of now minus 7 days. ok is false when none qualifies.
func pickRef7d(closes []DailyClose, now time.Time) (DailyClose, bool) {
	n := now.UTC()
	cutoff := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -7)
	var best DailyClose
	found := false
	for _, c := range closes {
		if c.Day.After(cutoff) {
			continue
		}
		if !found || c.Day.After(best.Day) {
			best, found = c, true
		}
	}
	return best, found
}
