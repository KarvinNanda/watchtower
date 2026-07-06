package sentinel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sentinelRoundTripFunc func(*http.Request) (*http.Response, error)

func (f sentinelRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// sentinelClientForServer returns an *http.Client that redirects every
// outgoing request to ts, regardless of the URL the caller actually dialed
// — needed because SentinelAnalyzer.callDeepSeek always targets the
// hardcoded production DeepSeek URL; only the http.Client is swappable via
// SetHTTPClient. Mirrors asset_test's clientForServer helper.
func sentinelClientForServer(ts *httptest.Server) *http.Client {
	return &http.Client{
		Transport: sentinelRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			req.URL.Scheme = "http"
			req.URL.Host = ts.Listener.Addr().String()
			return http.DefaultTransport.RoundTrip(req)
		}),
	}
}

func sentinelDeepSeekChoiceResponse(content string) []byte {
	body, _ := json.Marshal(map[string]interface{}{
		"choices": []map[string]interface{}{
			{"message": map[string]string{"role": "assistant", "content": content}},
		},
	})
	return body
}

func sampleSentinelItem() SentinelItem {
	return SentinelItem{
		SourceType:  "github_security_advisory",
		Identifier:  "GHSA-test-1234",
		Title:       "Remote code execution in example-package",
		Description: "A crafted payload allows arbitrary code execution.",
		URL:         "https://github.com/advisories/GHSA-test-1234",
		PublishedAt: time.Now(),
	}
}

// --- parseSentinelAnalysis / parseSentinelLabeledText / normalizeSentinelAnalysis ---

const validLabeledSentinelOutput = `--------------------------------------------------
STATUS BAHAYA: HIGH
CVE: CVE-2026-1234
KATEGORI: Layer 1 OS
DAMPAK: Perangkat dengan OS yang belum di-patch dapat diakses dari jarak jauh.
AKSI: jalankan: sudo apt update && sudo apt upgrade spice-vdagent
--------------------------------------------------`

func TestParseSentinelOutput_ValidFormat(t *testing.T) {
	t.Parallel()

	analysis, err := parseSentinelLabeledText(validLabeledSentinelOutput)
	require.NoError(t, err)
	require.NotNil(t, analysis)

	assert.Equal(t, "HIGH", analysis.StatusBahaya)
	assert.Equal(t, "CVE-2026-1234", analysis.CVE)
	assert.Equal(t, "Layer 1 OS", analysis.Kategori)
	assert.Contains(t, analysis.DampakID, "OS yang belum di-patch")
	assert.Contains(t, analysis.AksiID, "sudo apt update")
}

func TestParseSentinelOutput_MissingField(t *testing.T) {
	t.Parallel()

	withoutAksi := `STATUS BAHAYA: HIGH
CVE: CVE-2026-1234
KATEGORI: Layer 1 OS
DAMPAK: Perangkat dapat diakses dari jarak jauh.`

	analysis, err := parseSentinelLabeledText(withoutAksi)
	require.Error(t, err, "missing AKSI field should error, not panic")
	assert.Nil(t, analysis)
	assert.Contains(t, err.Error(), "AKSI")
}

func TestParseSentinelOutput_InvalidStatus(t *testing.T) {
	t.Parallel()

	analysis := normalizeSentinelAnalysis(&SentinelAnalysis{
		StatusBahaya: "UNKNOWN",
		AksiID:       "some action",
	})
	assert.Equal(t, "MEDIUM", analysis.StatusBahaya)
}

func TestParseSentinelOutput_CriticalStatus(t *testing.T) {
	t.Parallel()

	analysis := normalizeSentinelAnalysis(&SentinelAnalysis{
		StatusBahaya: "CRITICAL",
		AksiID:       "some action",
	})
	assert.Equal(t, "CRITICAL", analysis.StatusBahaya)
}

// --- AnalyzeItem end-to-end (mocked DeepSeek) ---

func TestAnalyzeSentinelItem_MockDeepSeek_StructuredOutput(t *testing.T) {
	t.Parallel()

	structuredJSON := `{"status_bahaya":"CRITICAL","cve":"CVE-2026-5678","kategori":"Layer 2 Gadget",` +
		`"dampak_id":"Dampak dalam Bahasa Indonesia","aksi_id":"jalankan: apt update",` +
		`"dampak_en":"Impact in English","aksi_en":"run: apt update"}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(sentinelDeepSeekChoiceResponse(structuredJSON))
	}))
	defer ts.Close()

	analyzer := NewSentinelAnalyzer("test-key", "deepseek-chat")
	analyzer.SetHTTPClient(sentinelClientForServer(ts))

	result, err := analyzer.AnalyzeItem(sampleSentinelItem(), nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "CRITICAL", result.StatusBahaya)
	assert.Equal(t, "CVE-2026-5678", result.CVE)
	assert.NotEmpty(t, result.DampakID)
	assert.NotEmpty(t, result.AksiID)
	assert.NotEmpty(t, result.DampakEN)
	assert.NotEmpty(t, result.AksiEN)
}

func TestAnalyzeSentinelItem_DeepSeekReturnsParagraph(t *testing.T) {
	t.Parallel()

	// DeepSeek ignoring the requested JSON/labeled-text format entirely and
	// returning a plain paragraph instead.
	paragraph := "Kerentanan ini cukup serius dan pengguna disarankan untuk segera memperbarui perangkat mereka."

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(sentinelDeepSeekChoiceResponse(paragraph))
	}))
	defer ts.Close()

	analyzer := NewSentinelAnalyzer("test-key", "deepseek-chat")
	analyzer.SetHTTPClient(sentinelClientForServer(ts))

	// Must not panic and must not error — a malformed AI response degrades
	// to a generic-but-deliverable notification rather than dropping the
	// whole item.
	result, err := analyzer.AnalyzeItem(sampleSentinelItem(), nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	// SentinelAnalysis no longer has a single free-text AnalysisID field
	// (see the Phase-10 structured-output rewrite) — DampakID is its
	// closest current analog, and is where the raw fallback text lands.
	assert.Contains(t, result.DampakID, "Kerentanan ini cukup serius")
	assert.Equal(t, "MEDIUM", result.StatusBahaya)
	assert.NotEmpty(t, result.AksiID)
}
