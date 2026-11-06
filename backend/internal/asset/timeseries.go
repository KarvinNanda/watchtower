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

// FetchStockCloses returns the last daily closes of a stock, newest
// first. It costs one Twelve Data credit. An empty list is an error.
func (f *AssetFetcher) FetchStockCloses(symbol string) ([]DailyClose, error) {
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
		return nil, fmt.Errorf("asset: build twelve data time series request for %s: %w", symbol, err)
	}
	req.Header.Set("Authorization", "apikey "+f.twelveDataKey)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("asset: fetch stock time series %s: %w", symbol, err)
	}
	defer resp.Body.Close()

	closes, err := decodeTwelveDataTimeSeries(resp.Body, symbol)
	if err != nil {
		return nil, err
	}
	if len(closes) == 0 {
		return nil, fmt.Errorf("asset: no daily closes for %s", symbol)
	}
	return closes, nil
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

// PickRef7d returns the close with the latest Day that is on or before
// sessionDay minus 7 days. sessionDay is the trading date the current
// price belongs to, so before the US open the reference follows the
// previous session, not the new UTC date. ok is false when none
// qualifies.
func PickRef7d(closes []DailyClose, sessionDay time.Time) (DailyClose, bool) {
	s := sessionDay.UTC()
	cutoff := time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -7)
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
