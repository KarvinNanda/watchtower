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
	"github.com/karvin-nanda/watchtower/internal/utils"
)

const (
	twelveDataQuoteURL      = "https://api.twelvedata.com/quote"
	defaultCoinGeckoBaseURL = "https://api.coingecko.com/api/v3"
	hargaEmasURL            = "https://harga-emas.org/"

	cryptoRateLimitDelay = 1 * time.Second
	stockRateLimitRetry  = 60 * time.Second

	stockDailyCallWarningThreshold = 800
)

// CoinGeckoBaseURL defaults to the public CoinGecko API but can be
// overridden (e.g. for a paid plan or a mock server in tests). CoinGecko's
// markets endpoint needs no API key, so this stays a package default
// rather than a constructor parameter.
var CoinGeckoBaseURL = defaultCoinGeckoBaseURL

// FetchResult is a normalized market price snapshot for a single asset
// symbol, regardless of source.
type FetchResult struct {
	Symbol   string
	PriceUSD float64
	// PriceIDR is set only for gold, which the source quotes in IDR.
	PriceIDR float64
	// ChangePct24h is nil when the source gives no usable 24h change.
	ChangePct24h *float64
	// Week52HighUSD and Week52LowUSD come from Twelve Data's /quote for
	// stocks. Crypto uses FetchCryptoWeek52; gold has no source.
	Week52HighUSD *float64
	Week52LowUSD  *float64
	// HighUSD is the source's recent high and HighSince the start of the
	// window it covers: crypto = the last 24 h, stock = the current (or
	// last) trading session. Both nil when unknown; gold has no source.
	HighUSD   *float64
	HighSince *time.Time
	Source    string
	FetchedAt time.Time
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
	stockPacer    *pacer
}

// NewAssetFetcher builds an AssetFetcher using twelveDataKey for Twelve
// Data stock quotes, with the standard hardened HTTP client (see
// utils.NewHTTPClient).
func NewAssetFetcher(twelveDataKey string) *AssetFetcher {
	return &AssetFetcher{
		twelveDataKey: twelveDataKey,
		httpClient:    utils.NewHTTPClient(),
		stockPacer:    newPacer(twelveDataMinGap),
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

	return decodeCoinGeckoMarkets(resp.Body, symbol, time.Now())
}

// decodeCoinGeckoMarkets parses a /coins/markets response. high_24h covers
// the 24 h before now, so its window starts at now - 24 h.
func decodeCoinGeckoMarkets(r io.Reader, symbol string, now time.Time) (*FetchResult, error) {
	var results []struct {
		CurrentPrice             float64  `json:"current_price"`
		PriceChangePercentage24h *float64 `json:"price_change_percentage_24h"`
		High24h                  *float64 `json:"high_24h"`
	}
	if err := json.NewDecoder(r).Decode(&results); err != nil {
		return nil, fmt.Errorf("asset: decode coingecko response for %s: %w", symbol, err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("asset: coingecko returned no data for %s", symbol)
	}

	res := &FetchResult{
		Symbol:       symbol,
		PriceUSD:     results[0].CurrentPrice,
		ChangePct24h: results[0].PriceChangePercentage24h,
		Source:       "coingecko",
		FetchedAt:    now,
	}
	if h := results[0].High24h; h != nil && *h > 0 {
		since := now.Add(-24 * time.Hour)
		res.HighUSD, res.HighSince = h, &since
	}
	return res, nil
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
// via Twelve Data's /quote (one API credit). On a rate-limit response it
// retries exactly once after waiting 60 seconds.
func (f *AssetFetcher) FetchStock(symbol string) (*FetchResult, error) {
	result, err := f.fetchStockOnce(symbol)
	if err != nil && errors.Is(err, errTwelveDataRateLimit) {
		log.Printf("[WARN] FetchStock: rate limit hit for %s, retrying in %s", symbol, stockRateLimitRetry)
		time.Sleep(stockRateLimitRetry)
		result, err = f.fetchStockOnce(symbol)
	}
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (f *AssetFetcher) fetchStockOnce(symbol string) (*FetchResult, error) {
	f.stockPacer.wait()
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

	return decodeTwelveDataQuote(resp.Body, symbol)
}

// decodeTwelveDataQuote parses a Twelve Data /quote response. Every numeric
// field arrives as a JSON string; a missing or unparsable optional field
// becomes nil rather than 0.
func decodeTwelveDataQuote(r io.Reader, symbol string) (*FetchResult, error) {
	var raw struct {
		Close         string `json:"close"`
		PercentChange string `json:"percent_change"`
		High          string `json:"high"`
		Timestamp     int64  `json:"timestamp"`
		FiftyTwoWeek  struct {
			High string `json:"high"`
			Low  string `json:"low"`
		} `json:"fifty_two_week"`
		Status  string `json:"status"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
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

	res := &FetchResult{
		Symbol:        symbol,
		PriceUSD:      price,
		ChangePct24h:  parseOptionalFloat(raw.PercentChange),
		Week52HighUSD: parseOptionalFloat(raw.FiftyTwoWeek.High),
		Week52LowUSD:  parseOptionalFloat(raw.FiftyTwoWeek.Low),
		Source:        "twelve_data",
		FetchedAt:     time.Now(),
	}
	// high is the session's high; timestamp is the session's start. A high
	// without its window start cannot be used safely, so both or neither.
	if h := parseOptionalFloat(raw.High); h != nil && *h > 0 && raw.Timestamp > 0 {
		since := time.Unix(raw.Timestamp, 0).UTC()
		res.HighUSD, res.HighSince = h, &since
	}
	return res, nil
}

func parseOptionalFloat(s string) *float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
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

	result, err := goldResult(priceIDR, currency.Current(), time.Now())
	if err != nil {
		log.Printf("[ERROR] FetchGold: %v", err)
		return nil, err
	}
	return result, nil
}

// goldResult converts the IDR gold price to USD. It refuses the currency
// package's hardcoded fallback rate: ANTAM rules are evaluated on PriceUSD,
// so a made-up rate could fire or miss them. The symbol is then skipped for
// this run instead.
func goldResult(priceIDR float64, rate currency.Rate, now time.Time) (*FetchResult, error) {
	if rate.Fallback || rate.Value <= 0 {
		return nil, fmt.Errorf("asset: no real usd/idr rate to convert gold price (fallback=%t, value=%v)", rate.Fallback, rate.Value)
	}
	return &FetchResult{
		Symbol:   "ANTAM",
		PriceUSD: priceIDR / rate.Value,
		PriceIDR: priceIDR,
		// harga-emas.org exposes only a point-in-time price. The cache layer
		// computes the 24h change from its own snapshots (price_snapshots).
		ChangePct24h: nil,
		Source:       "harga-emas.org",
		FetchedAt:    now,
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

// symbolAssetTypes is the whitelist of asset symbols this fetcher will ever
// call an external API for — mirroring the frontend's ASSET_OPTIONS list
// (frontend/src/constants/assets.js). Every caller (the scheduler via
// cache.Cache, cmd/test_fetch) reaches an external API only through
// FetchAsset, so checking here is a single, low-risk chokepoint that stops
// an arbitrary/malformed symbol from ever being sent to CoinGecko/Twelve
// Data, rather than relying solely on the format-only checks upstream in
// internal/user's subscription validation.
var symbolAssetTypes = map[string]string{
	"BTC": "crypto", "ETH": "crypto", "BNB": "crypto", "SOL": "crypto",
	"XRP": "crypto", "DOGE": "crypto", "ADA": "crypto", "TRX": "crypto",
	"AVAX": "crypto", "SHIB": "crypto", "TON": "crypto", "LINK": "crypto",
	"DOT": "crypto", "MATIC": "crypto", "DAI": "crypto", "UNI": "crypto",
	"ATOM": "crypto", "LTC": "crypto", "BCH": "crypto", "NEAR": "crypto",
	"AAPL": "stock", "MSFT": "stock", "NVDA": "stock", "GOOGL": "stock",
	"AMZN": "stock", "META": "stock", "TSLA": "stock", "NFLX": "stock",
	"TSM": "stock", "AVGO": "stock", "JPM": "stock", "LLY": "stock",
	"V": "stock", "UNH": "stock", "XOM": "stock", "MA": "stock",
	"JNJ": "stock", "PG": "stock", "HD": "stock", "COST": "stock",
	"ANTAM": "gold",
}

// SymbolAssetType reports which asset type a whitelisted symbol belongs to.
// It is the single source of truth for the API's symbol validation and for
// FetchAsset's guard.
func SymbolAssetType(symbol string) (string, bool) {
	t, ok := symbolAssetTypes[strings.ToUpper(symbol)]
	return t, ok
}

// FetchAsset routes to the correct fetch method based on assetType
// ("stock", "crypto", or "gold"), after checking symbol against
// symbolAssetTypes (see SymbolAssetType).
func (f *AssetFetcher) FetchAsset(symbol, assetType string) (*FetchResult, error) {
	if t, ok := SymbolAssetType(symbol); !ok || t != assetType {
		return nil, fmt.Errorf("asset: unsupported symbol %s for asset type %s", symbol, assetType)
	}

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

// maxMarketChartBytes caps the market_chart body (about 38 KB in practice).
const maxMarketChartBytes = 1 << 20

// FetchCryptoWeek52 returns the highest and lowest USD price of a crypto
// ticker over the last 365 days from CoinGecko's market_chart endpoint.
// The values are daily prices, not intraday highs and lows (spec §2).
func (f *AssetFetcher) FetchCryptoWeek52(symbol string) (high, low float64, err error) {
	if t, ok := SymbolAssetType(symbol); !ok || t != "crypto" {
		return 0, 0, fmt.Errorf("asset: unsupported crypto symbol: %s", symbol)
	}
	time.Sleep(cryptoRateLimitDelay)

	endpoint := fmt.Sprintf("%s/coins/%s/market_chart?vs_currency=usd&days=365",
		CoinGeckoBaseURL, url.PathEscape(coinGeckoID(symbol)))
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("asset: build coingecko market_chart request for %s: %w", symbol, err)
	}
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("asset: fetch coingecko market_chart for %s: %w", symbol, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("asset: coingecko market_chart returned status %d for %s", resp.StatusCode, symbol)
	}
	return decodeMarketChartRange(io.LimitReader(resp.Body, maxMarketChartBytes))
}

// decodeMarketChartRange returns max and min of prices[*][1], skipping
// malformed points.
func decodeMarketChartRange(r io.Reader) (high, low float64, err error) {
	var raw struct {
		Prices [][]float64 `json:"prices"`
	}
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return 0, 0, fmt.Errorf("asset: decode coingecko market_chart: %w", err)
	}
	found := false
	for _, p := range raw.Prices {
		if len(p) < 2 {
			continue
		}
		if !found || p[1] > high {
			high = p[1]
		}
		if !found || p[1] < low {
			low = p[1]
		}
		found = true
	}
	if !found {
		return 0, 0, errors.New("asset: coingecko market_chart has no prices")
	}
	return high, low, nil
}
