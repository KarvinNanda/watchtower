// Package currency provides the USD -> IDR exchange rate from an
// open.er-api.com-compatible endpoint, cached in-memory for up to an hour.
package currency

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

const (
	defaultBaseURL   = "https://open.er-api.com/v6/latest/USD"
	rateCacheTTL     = 1 * time.Hour
	fallbackUSDToIDR = 16000.0
)

var (
	httpClient = &http.Client{Timeout: 30 * time.Second}

	// BaseURL is the exchange-rate API endpoint. Overridable (e.g. from
	// config or tests) before the first call to GetUSDToIDR.
	BaseURL = defaultBaseURL

	cachedRate *ExchangeRate
	rateMu     sync.RWMutex
)

// ExchangeRate is a cached USD -> IDR exchange rate snapshot. UpdatedAt is
// the provider's own update time; FetchedAt is when this process fetched it.
type ExchangeRate struct {
	Rate      float64
	UpdatedAt time.Time
	FetchedAt time.Time
}

// Rate is a USD -> IDR rate for callers that show or compute prices.
// UpdatedAt is when the provider last updated it (zero if unknown).
// Fallback marks the hardcoded rate used when no real rate was ever
// fetched: it must not be shown as real or used to evaluate rules.
type Rate struct {
	Value     float64
	UpdatedAt time.Time
	Fallback  bool
}

// SetHTTPClient overrides the package's HTTP client, primarily for tests.
func SetHTTPClient(client *http.Client) {
	httpClient = client
}

type ratesResponse struct {
	Result             string             `json:"result"`
	TimeLastUpdateUnix int64              `json:"time_last_update_unix"`
	Rates              map[string]float64 `json:"rates"`
}

// Current returns the USD -> IDR rate. It serves from an in-memory cache
// refreshed at most once per hour (the free provider itself updates once a
// day). If the upstream call fails it returns the last real rate with its
// original UpdatedAt, and only when nothing was ever cached the hardcoded
// rate flagged as Fallback.
func Current() Rate {
	if r, ok := freshCachedRate(); ok {
		return r
	}

	r, err := fetchRate()
	if err != nil {
		log.Printf("[WARN] currency.Current: fetch failed, falling back: %v", err)
		if r, ok := anyCachedRate(); ok {
			return r
		}
		log.Printf("[WARN] currency.Current: no cached rate available, using hardcoded fallback %.2f", fallbackUSDToIDR)
		return Rate{Value: fallbackUSDToIDR, Fallback: true}
	}

	rateMu.Lock()
	cachedRate = &ExchangeRate{Rate: r.Value, UpdatedAt: r.UpdatedAt, FetchedAt: time.Now()}
	rateMu.Unlock()
	return r
}

// GetUSDToIDR returns Current().Value, including the hardcoded fallback.
// Use Current when the caller must know whether the rate is real.
func GetUSDToIDR() (float64, error) {
	return Current().Value, nil
}

func freshCachedRate() (Rate, bool) {
	rateMu.RLock()
	defer rateMu.RUnlock()

	if cachedRate != nil && time.Since(cachedRate.FetchedAt) < rateCacheTTL {
		return Rate{Value: cachedRate.Rate, UpdatedAt: cachedRate.UpdatedAt}, true
	}
	return Rate{}, false
}

func anyCachedRate() (Rate, bool) {
	rateMu.RLock()
	defer rateMu.RUnlock()

	if cachedRate != nil {
		return Rate{Value: cachedRate.Rate, UpdatedAt: cachedRate.UpdatedAt}, true
	}
	return Rate{}, false
}

func fetchRate() (Rate, error) {
	req, err := http.NewRequest(http.MethodGet, BaseURL, nil)
	if err != nil {
		return Rate{}, fmt.Errorf("currency: build request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return Rate{}, fmt.Errorf("currency: fetch rates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Rate{}, fmt.Errorf("currency: unexpected status %d", resp.StatusCode)
	}

	var parsed ratesResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Rate{}, fmt.Errorf("currency: decode response: %w", err)
	}

	if parsed.Result != "success" {
		return Rate{}, fmt.Errorf("currency: rate provider returned result=%q", parsed.Result)
	}

	rate, ok := parsed.Rates["IDR"]
	if !ok || rate <= 0 {
		return Rate{}, fmt.Errorf("currency: IDR rate not found in response")
	}

	r := Rate{Value: rate}
	if parsed.TimeLastUpdateUnix > 0 {
		r.UpdatedAt = time.Unix(parsed.TimeLastUpdateUnix, 0).UTC()
	}
	return r, nil
}

// ConvertToIDR converts a USD amount to IDR using the current cached/live
// USD -> IDR exchange rate.
func ConvertToIDR(usd float64) (float64, error) {
	rate, err := GetUSDToIDR()
	if err != nil {
		return 0, err
	}
	return usd * rate, nil
}
