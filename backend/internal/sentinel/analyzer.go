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

// SentinelAnalysis holds bilingual AI-generated commentary for a sentinel
// item.
type SentinelAnalysis struct {
	AnalysisID string
	AnalysisEN string
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
	AnalysisID string `json:"analysis_id"`
	AnalysisEN string `json:"analysis_en"`
}

const sentinelSystemPrompt = "Kamu adalah security analyst. Analisis item keamanan berikut dan berikan insight yang actionable. Response HANYA dalam format JSON, tanpa markdown, tanpa preamble."

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
		AnalysisID: parsed.AnalysisID,
		AnalysisEN: parsed.AnalysisEN,
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
  "analysis_id": "analisis dalam Bahasa Indonesia, max 150 kata, fokus: dampak spesifik ke devices dan OS user, langkah mitigasi sesuai expertise level, tingkat urgency (LOW/MEDIUM/HIGH/CRITICAL)",
  "analysis_en": "same analysis in English, max 150 kata"
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
