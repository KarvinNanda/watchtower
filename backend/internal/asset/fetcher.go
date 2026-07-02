// Package asset fetches live market data for stocks, crypto, and gold, and
// evaluates user subscriptions against that data for alert conditions.
package asset

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/karvin-nanda/watchtower/internal/currency"
)

const (
	twelveDataQuoteURL      = "https://api.twelvedata.com/quote"
	defaultCoinGeckoBaseURL = "https://api.coingecko.com/api/v3"
	hargaEmasURL            = "https://harga-emas.org/"

	cryptoRateLimitDelay = 1 * time.Second
	stockRateLimitRetry  = 60 * time.Second

	stockDailyCallWarningThreshold = 800

	fetcherHTTPTimeout = 30 * time.Second
)

// CoinGeckoBaseURL defaults to the public CoinGecko API but can be
// overridden (e.g. for a paid plan or a mock server in tests). CoinGecko's
// markets endpoint needs no API key, so this stays a package default
// rather than a constructor parameter.
var CoinGeckoBaseURL = defaultCoinGeckoBaseURL

// FetchResult is a normalized market price snapshot for a single asset
// symbol, regardless of source.
type FetchResult struct {
	Symbol       string
	PriceUSD     float64
	ChangePct24h float64
	Source       string
	FetchedAt    time.Time
}

// AssetFetcher fetches live market data for stocks, crypto, and gold. The
// Twelve Data API key and HTTP client are instance fields rather than
// package-level globals, so callers control exactly when and how the key
// is supplied — e.g. after godotenv.Load has run in main — instead of
// relying on package-init timing (which previously left the key empty at
// runtime).
type AssetFetcher struct {
	twelveDataKey string
	httpClient    *http.Client
}

// NewAssetFetcher builds an AssetFetcher using twelveDataKey for Twelve
// Data stock quotes, with a 30-second HTTP timeout.
func NewAssetFetcher(twelveDataKey string) *AssetFetcher {
	return &AssetFetcher{
		twelveDataKey: twelveDataKey,
		httpClient:    &http.Client{Timeout: fetcherHTTPTimeout},
	}
}

// SetHTTPClient overrides the fetcher's HTTP client, primarily for tests.
func (f *AssetFetcher) SetHTTPClient(client *http.Client) {
	f.httpClient = client
}

// cryptoSymbolToCoinGeckoID maps the ticker symbols WatchTower users
// subscribe with to CoinGecko coin IDs.
var cryptoSymbolToCoinGeckoID = map[string]string{
	"BTC": "bitcoin",
	"ETH": "ethereum",
	"SOL": "solana",
	"XRP": "xrp",
}

func coinGeckoID(symbol string) string {
	if id, ok := cryptoSymbolToCoinGeckoID[strings.ToUpper(symbol)]; ok {
		return id
	}
	// Fall back to treating the symbol as an already-valid CoinGecko ID.
	return strings.ToLower(symbol)
}

// FetchCrypto retrieves the latest quote for a crypto ticker (e.g. "BTC")
// via CoinGecko's /coins/markets endpoint. It sleeps briefly beforehand to
// stay within CoinGecko's free-tier rate limit of roughly 1 request/second.
func (f *AssetFetcher) FetchCrypto(symbol string) (*FetchResult, error) {
	time.Sleep(cryptoRateLimitDelay)

	coinID := coinGeckoID(symbol)
	endpoint := fmt.Sprintf("%s/coins/markets?vs_currency=usd&ids=%s", CoinGeckoBaseURL, url.QueryEscape(coinID))

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("asset: build coingecko request for %s: %w", symbol, err)
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("asset: fetch crypto %s: %w", symbol, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("asset: coingecko returned status %d for %s", resp.StatusCode, symbol)
	}

	var results []struct {
		CurrentPrice             float64 `json:"current_price"`
		PriceChangePercentage24h float64 `json:"price_change_percentage_24h"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("asset: decode coingecko response for %s: %w", symbol, err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("asset: coingecko returned no data for %s", symbol)
	}

	return &FetchResult{
		Symbol:       symbol,
		PriceUSD:     results[0].CurrentPrice,
		ChangePct24h: results[0].PriceChangePercentage24h,
		Source:       "coingecko",
		FetchedAt:    time.Now(),
	}, nil
}

var errTwelveDataRateLimit = errors.New("twelve data rate limit exceeded")

var (
	stockCallMu    sync.Mutex
	stockCallCount int
	stockCallDate  string
)

func recordStockAPICall() {
	stockCallMu.Lock()
	defer stockCallMu.Unlock()

	today := time.Now().Format("2006-01-02")
	if today != stockCallDate {
		stockCallDate = today
		stockCallCount = 0
	}
	stockCallCount++

	if stockCallCount == stockDailyCallWarningThreshold {
		log.Printf("[WARN] FetchStock: reached %d Twelve Data calls today — approaching the 800 calls/day free-tier limit", stockCallCount)
	}
}

// FetchStock retrieves the latest quote for a stock ticker (e.g. "AAPL")
// via Twelve Data. On a rate-limit response it retries exactly once after
// waiting 60 seconds.
func (f *AssetFetcher) FetchStock(symbol string) (*FetchResult, error) {
	result, err := f.fetchStockOnce(symbol)
	if err != nil && errors.Is(err, errTwelveDataRateLimit) {
		log.Printf("[WARN] FetchStock: rate limit hit for %s, retrying in %s", symbol, stockRateLimitRetry)
		time.Sleep(stockRateLimitRetry)
		result, err = f.fetchStockOnce(symbol)
	}
	return result, err
}

func (f *AssetFetcher) fetchStockOnce(symbol string) (*FetchResult, error) {
	recordStockAPICall()

	endpoint := fmt.Sprintf("%s?symbol=%s&apikey=%s",
		twelveDataQuoteURL, url.QueryEscape(symbol), url.QueryEscape(f.twelveDataKey))

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("asset: build twelve data request for %s: %w", symbol, err)
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("asset: fetch stock %s: %w", symbol, err)
	}
	defer resp.Body.Close()

	var raw struct {
		Close         string `json:"close"`
		PercentChange string `json:"percent_change"`
		Status        string `json:"status"`
		Code          int    `json:"code"`
		Message       string `json:"message"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("asset: decode twelve data response for %s: %w", symbol, err)
	}

	if raw.Status == "error" {
		if raw.Code == http.StatusTooManyRequests || strings.Contains(strings.ToLower(raw.Message), "rate limit") {
			return nil, fmt.Errorf("asset: %w: %s", errTwelveDataRateLimit, raw.Message)
		}
		return nil, fmt.Errorf("asset: twelve data error for %s: %s", symbol, raw.Message)
	}

	price, err := strconv.ParseFloat(raw.Close, 64)
	if err != nil {
		return nil, fmt.Errorf("asset: parse price for %s: %w", symbol, err)
	}
	changePct, err := strconv.ParseFloat(raw.PercentChange, 64)
	if err != nil {
		changePct = 0
	}

	return &FetchResult{
		Symbol:       symbol,
		PriceUSD:     price,
		ChangePct24h: changePct,
		Source:       "twelve_data",
		FetchedAt:    time.Now(),
	}, nil
}

// jsonLDPattern extracts the contents of every <script type="application/ld+json">
// block on a page. JSON-LD structured data is used for SEO and is far more
// stable across deploys than CSS class names (which harga-emas.org
// hashes per build), so it is preferred here over DOM scraping.
var jsonLDPattern = regexp.MustCompile(`(?s)<script[^>]+type="application/ld\+json"[^>]*>(.*?)</script>`)

type goldProductLD struct {
	Type   string `json:"@type"`
	Offers struct {
		Price         float64 `json:"price"`
		PriceCurrency string  `json:"priceCurrency"`
	} `json:"offers"`
}

// FetchGold scrapes the current Indonesian Antam gold price (in IDR **per
// gram**, matching local retail convention) from harga-emas.org's JSON-LD
// structured data and converts it to USD using the live USD/IDR exchange
// rate. The returned PriceUSD is therefore USD-per-gram, not the
// USD-per-troy-ounce convention used by international XAU/USD quotes
// (1 troy oz = 31.1034768 g) — do not compare it directly against
// XAU/USD spot prices. It deliberately does not fall back to a generic
// XAU/USD spot price — callers need the Indonesian retail price
// specifically.
func (f *AssetFetcher) FetchGold() (*FetchResult, error) {
	priceIDR, err := f.fetchGoldPriceIDR()
	if err != nil {
		log.Printf("[ERROR] FetchGold: %v", err)
		return nil, fmt.Errorf("asset: fetch gold price: %w", err)
	}

	rate, err := currency.GetUSDToIDR()
	if err != nil {
		log.Printf("[ERROR] FetchGold: get usd/idr rate: %v", err)
		return nil, fmt.Errorf("asset: convert gold price to usd: %w", err)
	}
	if rate == 0 {
		err := fmt.Errorf("asset: usd/idr rate is zero")
		log.Printf("[ERROR] FetchGold: %v", err)
		return nil, err
	}

	return &FetchResult{
		Symbol:   "XAU",
		PriceUSD: priceIDR / rate,
		// harga-emas.org's JSON-LD structured data exposes only a single
		// point-in-time price, with no 24h delta available to compute here.
		ChangePct24h: 0,
		Source:       "harga-emas.org",
		FetchedAt:    time.Now(),
	}, nil
}

func (f *AssetFetcher) fetchGoldPriceIDR() (float64, error) {
	req, err := http.NewRequest(http.MethodGet, hargaEmasURL, nil)
	if err != nil {
		return 0, fmt.Errorf("build harga-emas.org request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; WatchTowerBot/1.0)")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch harga-emas.org: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("harga-emas.org returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read harga-emas.org response: %w", err)
	}

	return extractGoldPriceIDR(body)
}

func extractGoldPriceIDR(body []byte) (float64, error) {
	matches := jsonLDPattern.FindAllSubmatch(body, -1)
	if len(matches) == 0 {
		return 0, errors.New("no JSON-LD script tags found on harga-emas.org")
	}

	for _, m := range matches {
		var ld goldProductLD
		if err := json.Unmarshal(m[1], &ld); err != nil {
			continue
		}
		if ld.Type == "Product" && ld.Offers.PriceCurrency == "IDR" && ld.Offers.Price > 0 {
			return ld.Offers.Price, nil
		}
	}

	return 0, errors.New("gold price not found in harga-emas.org JSON-LD data")
}

// FetchAsset routes to the correct fetch method based on assetType
// ("stock", "crypto", or "gold").
func (f *AssetFetcher) FetchAsset(symbol, assetType string) (*FetchResult, error) {
	switch assetType {
	case "crypto":
		return f.FetchCrypto(symbol)
	case "stock":
		return f.FetchStock(symbol)
	case "gold":
		return f.FetchGold()
	default:
		return nil, fmt.Errorf("asset: unknown asset type %q", assetType)
	}
}
