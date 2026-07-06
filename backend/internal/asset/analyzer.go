package asset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/karvin-nanda/watchtower/internal/utils"
)

// AlertType mirrors the asset_subscriptions.alert_type enum.
type AlertType string

const (
	AlertTypePriceThreshold AlertType = "price_threshold"
	AlertTypePctChange      AlertType = "pct_change"
	AlertTypeBoth           AlertType = "both"
)

// TriggeredAlertType mirrors the alert_states.last_alert_type enum.
type TriggeredAlertType string

const (
	TriggeredLower     TriggeredAlertType = "lower"
	TriggeredUpper     TriggeredAlertType = "upper"
	TriggeredPctChange TriggeredAlertType = "pct_change"
)

// Subscription is the subset of asset_subscriptions needed to evaluate
// alert conditions.
type Subscription struct {
	UserID             uint64
	AssetSymbol        string
	AlertType          AlertType
	PriceLowerUSD      *float64
	PriceUpperUSD      *float64
	PctChangeThreshold *float64
}

// AlertState is the subset of alert_states needed to evaluate cooldowns.
type AlertState struct {
	LastAlertedPriceUSD *float64
	CooldownUntil       *time.Time
}

// Result describes an alert that should be fired for a subscription.
type Result struct {
	Type      TriggeredAlertType
	PriceUSD  float64
	ChangePct float64
}

// Analyzer evaluates asset subscriptions against fresh market data.
type Analyzer struct{}

// NewAnalyzer builds an Analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{}
}

// Evaluate returns the alert that should fire for sub given data and the
// user's current alert state, or nil if no alert should fire — either
// because no threshold is breached, or because the alert is still within
// its cooldown window.
func (a *Analyzer) Evaluate(sub Subscription, data FetchResult, state *AlertState, now time.Time) *Result {
	if state != nil && state.CooldownUntil != nil && now.Before(*state.CooldownUntil) {
		return nil
	}

	checkThreshold := sub.AlertType == AlertTypePriceThreshold || sub.AlertType == AlertTypeBoth
	checkPctChange := sub.AlertType == AlertTypePctChange || sub.AlertType == AlertTypeBoth

	if checkThreshold {
		if sub.PriceLowerUSD != nil && data.PriceUSD <= *sub.PriceLowerUSD {
			return &Result{Type: TriggeredLower, PriceUSD: data.PriceUSD, ChangePct: data.ChangePct24h}
		}
		if sub.PriceUpperUSD != nil && data.PriceUSD >= *sub.PriceUpperUSD {
			return &Result{Type: TriggeredUpper, PriceUSD: data.PriceUSD, ChangePct: data.ChangePct24h}
		}
	}

	if checkPctChange && sub.PctChangeThreshold != nil {
		if absFloat(data.ChangePct24h) >= *sub.PctChangeThreshold {
			return &Result{Type: TriggeredPctChange, PriceUSD: data.PriceUSD, ChangePct: data.ChangePct24h}
		}
	}

	return nil
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// SubscriberContext is the subset of an asset_subscriptions row needed to
// give DeepSeek enough context to judge whether any subscriber's threshold
// is under threat.
type SubscriberContext struct {
	AlertType          string
	PriceLowerUSD      *float64
	PriceUpperUSD      *float64
	PctChangeThreshold *float64
}

// AnalysisResult holds bilingual AI-generated commentary for a fetched
// asset price point.
type AnalysisResult struct {
	Symbol      string
	AnalysisID  string
	AnalysisEN  string
	GeneratedAt time.Time
}

const (
	deepSeekAPIURL      = "https://api.deepseek.com/v1/chat/completions"
	deepSeekMaxTokens   = 800
	deepSeekTemperature = 0.3
)

// DeepSeekAnalyzer produces bilingual AI market commentary via the
// DeepSeek chat completions API. The API key/model and HTTP client are
// instance fields rather than package-level globals so callers control
// exactly when and how credentials are supplied, and so tests can inject a
// mock client — the same reasoning applied to AssetFetcher.
type DeepSeekAnalyzer struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewDeepSeekAnalyzer builds a DeepSeekAnalyzer using apiKey/model, with the
// standard hardened HTTP client (see utils.NewHTTPClient).
func NewDeepSeekAnalyzer(apiKey, model string) *DeepSeekAnalyzer {
	return &DeepSeekAnalyzer{
		apiKey:     apiKey,
		model:      model,
		httpClient: utils.NewHTTPClient(),
	}
}

// SetHTTPClient overrides the analyzer's HTTP client, primarily for tests.
func (a *DeepSeekAnalyzer) SetHTTPClient(client *http.Client) {
	a.httpClient = client
}

type deepSeekMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type deepSeekRequest struct {
	Model       string            `json:"model"`
	Messages    []deepSeekMessage `json:"messages"`
	MaxTokens   int               `json:"max_tokens"`
	Temperature float64           `json:"temperature"`
}

type deepSeekResponse struct {
	Choices []struct {
		Message deepSeekMessage `json:"message"`
	} `json:"choices"`
}

type analysisJSON struct {
	AnalysisID string `json:"analysis_id"`
	AnalysisEN string `json:"analysis_en"`
}

// AnalyzeAsset asks DeepSeek to produce a bilingual (Indonesian/English)
// market commentary for data, taking into account how close the price is
// to any subscriber's alert thresholds. usdToIDR is the current USD->IDR
// exchange rate (from currency.GetUSDToIDR, fetched by the caller) — it's
// injected into the prompt both as the pre-computed IDR price and as an
// explicit instruction, so DeepSeek's own commentary can convert other USD
// figures it might mention using the same rate WatchTower actually used.
func (a *DeepSeekAnalyzer) AnalyzeAsset(data *FetchResult, subscribers []SubscriberContext, usdToIDR float64) (*AnalysisResult, error) {
	if a.apiKey == "" {
		return nil, fmt.Errorf("asset: DeepSeek API key is not configured")
	}

	priceIDR := data.PriceUSD * usdToIDR

	prompt := buildAnalysisPrompt(data, priceIDR, usdToIDR, subscribers)

	raw, err := a.callDeepSeek(prompt)
	if err != nil {
		log.Printf("[ERROR] AnalyzeAsset: call deepseek for %s: %v", data.Symbol, err)
		return nil, fmt.Errorf("asset: analyze %s: %w", data.Symbol, err)
	}

	var parsed analysisJSON
	if err := json.Unmarshal([]byte(extractJSON(raw)), &parsed); err != nil {
		log.Printf("[ERROR] AnalyzeAsset: parse deepseek response for %s: %v", data.Symbol, err)
		return nil, fmt.Errorf("asset: parse analysis for %s: %w", data.Symbol, err)
	}

	return &AnalysisResult{
		Symbol:      data.Symbol,
		AnalysisID:  parsed.AnalysisID,
		AnalysisEN:  parsed.AnalysisEN,
		GeneratedAt: time.Now(),
	}, nil
}

func buildAnalysisPrompt(data *FetchResult, priceIDR, usdToIDR float64, subscribers []SubscriberContext) string {
	var thresholds strings.Builder
	if len(subscribers) == 0 {
		thresholds.WriteString("Tidak ada subscriber dengan threshold spesifik saat ini.")
	} else {
		var lowers, uppers, pctChanges []string
		for _, s := range subscribers {
			if s.PriceLowerUSD != nil {
				lowers = append(lowers, fmt.Sprintf("$%.2f", *s.PriceLowerUSD))
			}
			if s.PriceUpperUSD != nil {
				uppers = append(uppers, fmt.Sprintf("$%.2f", *s.PriceUpperUSD))
			}
			if s.PctChangeThreshold != nil {
				pctChanges = append(pctChanges, fmt.Sprintf("%.2f%%", *s.PctChangeThreshold))
			}
		}
		if len(lowers) > 0 {
			fmt.Fprintf(&thresholds, "Lower bound subscriber: %s. ", strings.Join(lowers, ", "))
		}
		if len(uppers) > 0 {
			fmt.Fprintf(&thresholds, "Upper bound subscriber: %s. ", strings.Join(uppers, ", "))
		}
		if len(pctChanges) > 0 {
			fmt.Fprintf(&thresholds, "Threshold perubahan persentase subscriber: %s.", strings.Join(pctChanges, ", "))
		}
	}

	// The technical-indicator block is only available for stock symbols
	// (see enrichWithTechnicalIndicators in technical.go) — data.Signal is
	// left at its zero value "" for crypto/gold, which is used here as the
	// flag for whether to include the block at all, rather than showing
	// DeepSeek a block of misleading zeros for assets with no history
	// wired up.
	var technicalBlock string
	if data.Signal != "" {
		technicalBlock = fmt.Sprintf(`
Data teknikal per aset:
- RSI (14): %.2f — di bawah 30 oversold, di atas 70 overbought
- Volatilitas (annualized): %.2f%%
- Trend: %s (BULLISH/BEARISH/NEUTRAL)
- Signal: %s (BUY/SELL/HOLD)
- Range 14 hari: $%.2f - $%.2f
- Target beli: $%.2f | Target jual: $%.2f
`, data.RSI, data.Volatility, data.Trend, data.Signal, data.RangeLow14D, data.RangeHigh14D, data.TargetBuyUSD, data.TargetSellUSD)
	}

	return fmt.Sprintf(`Kamu adalah analis pasar untuk aplikasi monitoring harga WatchTower.

Data harga terkini untuk %s:
- Harga: $%.2f USD (Rp%.0f IDR)
- Perubahan 24 jam: %.2f%%
- Sumber data: %s
%s
Kurs saat ini: 1 USD = Rp %.0f (gunakan nilai ini untuk semua konversi).

Aturan format angka WAJIB diikuti:
- Semua angka gunakan separator ribuan dengan koma, contoh: $61,633.00 bukan $61633
- Semua harga USD sertakan konversi IDR, contoh: $61,633.00 (Rp 1,108,743,364)
- Jangan gunakan format angka tanpa separator

Konteks subscriber:
%s

Format analisis yang WAJIB diikuti:
1. Signal: BUY/SELL/HOLD dengan alasan 1 kalimat
2. Durasi hold yang disarankan
3. Level support dan resisten terdekat
4. Action item konkret dalam 24 jam
5. Satu peringatan risiko utama

Maksimal 150 kata per bahasa. Gunakan angka spesifik, bukan range yang terlalu lebar.

Balas HANYA dengan JSON valid tanpa markdown code fence, dengan struktur persis seperti ini:
{"analysis_id": "<analisis dalam Bahasa Indonesia>", "analysis_en": "<analysis in English>"}`,
		data.Symbol, data.PriceUSD, priceIDR, data.ChangePct24h, data.Source, technicalBlock, usdToIDR, thresholds.String())
}

func (a *DeepSeekAnalyzer) callDeepSeek(prompt string) (string, error) {
	reqBody := deepSeekRequest{
		Model: a.model,
		Messages: []deepSeekMessage{
			{Role: "user", Content: prompt},
		},
		MaxTokens:   deepSeekMaxTokens,
		Temperature: deepSeekTemperature,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, deepSeekAPIURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var parsed deepSeekResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("empty response from deepseek")
	}

	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

// extractJSON strips optional markdown code fences DeepSeek sometimes wraps
// its JSON output in, despite being asked not to.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
