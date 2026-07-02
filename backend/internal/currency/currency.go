// Package currency converts USD-denominated prices to IDR using a live
// exchange rate fetched from an open.er-api.com-compatible endpoint, cached
// in-memory for up to an hour.
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

// ExchangeRate is a cached USD -> IDR exchange rate snapshot.
type ExchangeRate struct {
	Rate      float64
	FetchedAt time.Time
}

// SetHTTPClient overrides the package's HTTP client, primarily for tests.
func SetHTTPClient(client *http.Client) {
	httpClient = client
}

type ratesResponse struct {
	Result string             `json:"result"`
	Rates  map[string]float64 `json:"rates"`
}

// GetUSDToIDR returns the current USD -> IDR exchange rate. It serves from
// an in-memory cache refreshed at most once per hour; on a cache miss it
// hits BaseURL. If the upstream call fails, it falls back to the last known
// cached rate if one exists, and finally to a hardcoded rate — logging a
// warning either way rather than failing the caller outright.
func GetUSDToIDR() (float64, error) {
	if rate, ok := freshCachedRate(); ok {
		return rate, nil
	}

	rate, err := fetchUSDToIDR()
	if err != nil {
		log.Printf("[WARN] GetUSDToIDR: fetch failed, falling back: %v", err)

		if rate, ok := anyCachedRate(); ok {
			return rate, nil
		}

		log.Printf("[WARN] GetUSDToIDR: no cached rate available, using hardcoded fallback %.2f", fallbackUSDToIDR)
		return fallbackUSDToIDR, nil
	}

	rateMu.Lock()
	cachedRate = &ExchangeRate{Rate: rate, FetchedAt: time.Now()}
	rateMu.Unlock()

	return rate, nil
}

func freshCachedRate() (float64, bool) {
	rateMu.RLock()
	defer rateMu.RUnlock()

	if cachedRate != nil && time.Since(cachedRate.FetchedAt) < rateCacheTTL {
		return cachedRate.Rate, true
	}
	return 0, false
}

func anyCachedRate() (float64, bool) {
	rateMu.RLock()
	defer rateMu.RUnlock()

	if cachedRate != nil {
		return cachedRate.Rate, true
	}
	return 0, false
}

func fetchUSDToIDR() (float64, error) {
	req, err := http.NewRequest(http.MethodGet, BaseURL, nil)
	if err != nil {
		return 0, fmt.Errorf("currency: build request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("currency: fetch rates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("currency: unexpected status %d", resp.StatusCode)
	}

	var parsed ratesResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, fmt.Errorf("currency: decode response: %w", err)
	}

	if parsed.Result != "success" {
		return 0, fmt.Errorf("currency: rate provider returned result=%q", parsed.Result)
	}

	rate, ok := parsed.Rates["IDR"]
	if !ok {
		return 0, fmt.Errorf("currency: IDR rate not found in response")
	}

	return rate, nil
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
