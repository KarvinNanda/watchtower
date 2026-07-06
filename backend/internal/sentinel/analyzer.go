package sentinel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	deepSeekAPIURL      = "https://api.deepseek.com/v1/chat/completions"
	deepSeekMaxTokens   = 600
	deepSeekTemperature = 0.2
	analyzerHTTPTimeout = 30 * time.Second
)

// UserContext aggregates the subscriber context relevant to a sentinel
// item — the matched keywords, their per-keyword notes, and the devices/
// OS/expertise of the users whose subscriptions matched — used to ground
// DeepSeek's analysis. Since DeepSeek is called once per item (not once
// per matching user), this represents the combined audience for that item
// rather than any single individual.
type UserContext struct {
	Keywords    []string
	ContextNote map[string]string
	Devices     []string
	OSList      []string
	Expertise   string
}

// SentinelAnalysis holds structured, bilingual AI-generated commentary for a
// sentinel item, designed to render as a compact scannable card in Telegram
// rather than a long paragraph.
type SentinelAnalysis struct {
	StatusBahaya string
	CVE          string
	Kategori     string
	DampakID     string
	AksiID       string
	DampakEN     string
	AksiEN       string
}

// SentinelAnalyzer produces bilingual security analysis via the DeepSeek
// chat completions API. Credentials and the HTTP client are instance
// fields rather than package-level globals, matching AssetFetcher/
// DeepSeekAnalyzer's dependency-injection pattern: explicit wiring,
// mockable in tests, no reliance on package-init timing.
type SentinelAnalyzer struct {
	deepseekKey   string
	deepseekModel string
	httpClient    *http.Client
}

// NewSentinelAnalyzer builds a SentinelAnalyzer using deepseekKey/Model,
// with a 30-second HTTP timeout.
func NewSentinelAnalyzer(deepseekKey, deepseekModel string) *SentinelAnalyzer {
	return &SentinelAnalyzer{
		deepseekKey:   deepseekKey,
		deepseekModel: deepseekModel,
		httpClient:    &http.Client{Timeout: analyzerHTTPTimeout},
	}
}

// SetHTTPClient overrides the analyzer's HTTP client, primarily for tests.
func (a *SentinelAnalyzer) SetHTTPClient(client *http.Client) {
	a.httpClient = client
}

type sentinelChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type sentinelChatRequest struct {
	Model       string                `json:"model"`
	Messages    []sentinelChatMessage `json:"messages"`
	MaxTokens   int                   `json:"max_tokens"`
	Temperature float64               `json:"temperature"`
}

type sentinelChatResponse struct {
	Choices []struct {
		Message sentinelChatMessage `json:"message"`
	} `json:"choices"`
}

type sentinelAnalysisJSON struct {
	StatusBahaya string `json:"status_bahaya"`
	CVE          string `json:"cve"`
	Kategori     string `json:"kategori"`
	DampakID     string `json:"dampak_id"`
	AksiID       string `json:"aksi_id"`
	DampakEN     string `json:"dampak_en"`
	AksiEN       string `json:"aksi_en"`
}

// sentinelSystemPrompt asks for structured fields rather than a free-form
// paragraph so the result renders as a compact, scannable card in Telegram
// (see buildSentinelItemBlock in scheduler/sentinel_worker.go). The fields
// are requested as JSON — not as raw delimited text — for the same reason
// the rest of this codebase's DeepSeek prompts use JSON (see
// asset.DeepSeekAnalyzer.AnalyzeAsset): free-form text parsing is fragile
// against formatting drift, and dampak/aksi need both an Indonesian and an
// English version to support this app's per-user preferred_language.
const sentinelSystemPrompt = `Kamu adalah Senior Security Intelligence Analyst. Analisis item keamanan berikut dan hasilkan laporan singkat yang scannable, dengan field-field berikut:

- status_bahaya: salah satu dari CRITICAL, HIGH, MEDIUM, atau LOW
- cve: CVE ID yang disebutkan di item, atau "N/A" jika tidak ada
- kategori: salah satu dari "Layer 1 OS", "Layer 2 Gadget", "Layer 3 Finansial", atau "Layer 4 Social Eng"
- dampak_id / dampak_en: 1-2 kalimat, apa yang terjadi jika user terkena — spesifik ke context user (devices, OS, expertise) yang diberikan, dalam Bahasa Indonesia dan English
- aksi_id / aksi_en: 1-2 kalimat, langkah konkret yang bisa dilakukan sekarang — actionable, bukan "update software" tapi contoh "jalankan: sudo apt update && sudo apt upgrade spice-vdagent", dalam Bahasa Indonesia dan English

Response HANYA dalam format JSON, tanpa markdown, tanpa preamble.`

// AnalyzeItem asks DeepSeek to produce bilingual, actionable security
// analysis for item, grounded in userContext — the aggregated devices/OS/
// expertise/keywords of the users whose subscriptions matched this item.
// userContext may be nil if no aggregated context is available.
func (a *SentinelAnalyzer) AnalyzeItem(item SentinelItem, userContext *UserContext) (*SentinelAnalysis, error) {
	if a.deepseekKey == "" {
		return nil, fmt.Errorf("sentinel: DeepSeek API key is not configured")
	}

	prompt := buildSentinelPrompt(item, userContext)

	raw, err := a.callDeepSeek(prompt)
	if err != nil {
		return nil, fmt.Errorf("sentinel: analyze %s: %w", item.Identifier, err)
	}

	var parsed sentinelAnalysisJSON
	if err := json.Unmarshal([]byte(stripMarkdownFence(raw)), &parsed); err != nil {
		return nil, fmt.Errorf("sentinel: parse analysis for %s: %w", item.Identifier, err)
	}

	return &SentinelAnalysis{
		StatusBahaya: parsed.StatusBahaya,
		CVE:          parsed.CVE,
		Kategori:     parsed.Kategori,
		DampakID:     parsed.DampakID,
		AksiID:       parsed.AksiID,
		DampakEN:     parsed.DampakEN,
		AksiEN:       parsed.AksiEN,
	}, nil
}

func buildSentinelPrompt(item SentinelItem, userContext *UserContext) string {
	keywords := "-"
	contextNotes := "-"
	devices := "-"
	osList := "-"
	expertise := "beginner"

	if userContext != nil {
		if len(userContext.Keywords) > 0 {
			keywords = strings.Join(userContext.Keywords, ", ")
		}
		if len(userContext.ContextNote) > 0 {
			var notes []string
			for kw, note := range userContext.ContextNote {
				if note == "" {
					continue
				}
				notes = append(notes, fmt.Sprintf("%s: %s", kw, note))
			}
			if len(notes) > 0 {
				contextNotes = strings.Join(notes, "; ")
			}
		}
		if len(userContext.Devices) > 0 {
			devices = strings.Join(userContext.Devices, ", ")
		}
		if len(userContext.OSList) > 0 {
			osList = strings.Join(userContext.OSList, ", ")
		}
		if userContext.Expertise != "" {
			expertise = userContext.Expertise
		}
	}

	userPrompt := fmt.Sprintf(`Item: %s
Source: %s
Description: %s
URL: %s

User context:
- Keywords yang dimonitor: %s
- Context per keyword: %s
- Devices: %s
- OS: %s
- Expertise level: %s

Berikan analisis dalam JSON:
{
  "status_bahaya": "CRITICAL/HIGH/MEDIUM/LOW",
  "cve": "CVE ID atau N/A",
  "kategori": "Layer 1 OS / Layer 2 Gadget / Layer 3 Finansial / Layer 4 Social Eng",
  "dampak_id": "dampak dalam Bahasa Indonesia, spesifik ke devices/OS/expertise user, max 2 kalimat",
  "aksi_id": "aksi konkret dalam Bahasa Indonesia, max 2 kalimat",
  "dampak_en": "same impact in English, max 2 sentences",
  "aksi_en": "same action in English, max 2 sentences"
}`,
		item.Title, item.SourceType, item.Description, item.URL,
		keywords, contextNotes, devices, osList, expertise,
	)

	return userPrompt
}

func (a *SentinelAnalyzer) callDeepSeek(userPrompt string) (string, error) {
	reqBody := sentinelChatRequest{
		Model: a.deepseekModel,
		Messages: []sentinelChatMessage{
			{Role: "system", Content: sentinelSystemPrompt},
			{Role: "user", Content: userPrompt},
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
	req.Header.Set("Authorization", "Bearer "+a.deepseekKey)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var parsed sentinelChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("empty response from deepseek")
	}

	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

// stripMarkdownFence strips optional markdown code fences DeepSeek
// sometimes wraps its JSON output in, despite being asked not to.
func stripMarkdownFence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
