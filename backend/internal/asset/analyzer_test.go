package asset_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/asset"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// clientForServer returns an *http.Client that redirects every outgoing
// request to ts, regardless of the URL the caller actually dialed. This is
// needed because DeepSeekAnalyzer.callDeepSeek always targets the hardcoded
// production DeepSeek URL; only the http.Client is swappable via
// SetHTTPClient.
func clientForServer(ts *httptest.Server) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			req.URL.Scheme = "http"
			req.URL.Host = ts.Listener.Addr().String()
			return http.DefaultTransport.RoundTrip(req)
		}),
	}
}

func deepSeekChoiceResponse(content string) []byte {
	body, _ := json.Marshal(map[string]interface{}{
		"choices": []map[string]interface{}{
			{"message": map[string]string{"role": "assistant", "content": content}},
		},
	})
	return body
}

func sampleFetchResult() *asset.FetchResult {
	return &asset.FetchResult{
		Symbol:       "BTC",
		PriceUSD:     65000,
		ChangePct24h: 2.5,
		Source:       "coingecko",
		FetchedAt:    time.Now(),
	}
}

func TestAnalyzeAsset_MockDeepSeek(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(deepSeekChoiceResponse(`{"analysis_id":"Analisis singkat","analysis_en":"Short analysis"}`))
	}))
	defer ts.Close()

	analyzer := asset.NewDeepSeekAnalyzer("test-key", "deepseek-chat")
	analyzer.SetHTTPClient(clientForServer(ts))

	result, err := analyzer.AnalyzeAsset(sampleFetchResult(), nil, 16000)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "BTC", result.Symbol)
	assert.NotEmpty(t, result.AnalysisID)
	assert.NotEmpty(t, result.AnalysisEN)
}

func TestAnalyzeAsset_DeepSeekDown(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	analyzer := asset.NewDeepSeekAnalyzer("test-key", "deepseek-chat")
	analyzer.SetHTTPClient(clientForServer(ts))

	result, err := analyzer.AnalyzeAsset(sampleFetchResult(), nil, 16000)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "503")
}

func TestAnalyzeAsset_InvalidJSON(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(deepSeekChoiceResponse(`this is not valid JSON at all`))
	}))
	defer ts.Close()

	analyzer := asset.NewDeepSeekAnalyzer("test-key", "deepseek-chat")
	analyzer.SetHTTPClient(clientForServer(ts))

	result, err := analyzer.AnalyzeAsset(sampleFetchResult(), nil, 16000)

	require.Error(t, err)
	assert.Nil(t, result)
}
