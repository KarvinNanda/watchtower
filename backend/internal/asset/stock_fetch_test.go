package asset

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingTransport answers every request with body and records its URL,
// whatever the host, so no test request can leave the machine.
type recordingTransport struct {
	body []byte
	urls []string
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.urls = append(rt.urls, r.URL.Host+r.URL.Path)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(rt.body)), Header: http.Header{}}, nil
}

// One stock fetch must cost one Twelve Data credit: /quote only, no
// /time_series (#20, spec decision 1).
func TestFetchStock_MakesExactlyOneRequest(t *testing.T) {
	fixture, err := os.ReadFile("testdata/twelvedata_quote_aapl.json")
	require.NoError(t, err)
	rt := &recordingTransport{body: fixture}
	f := NewAssetFetcher("test-key")
	f.SetHTTPClient(&http.Client{Transport: rt})

	res, err := f.FetchStock("AAPL")

	require.NoError(t, err)
	assert.Equal(t, []string{"api.twelvedata.com/quote"}, rt.urls)
	assert.Equal(t, 342.95999, res.PriceUSD)
}
