package asset

import (
	"bytes"
	"errors"
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

// requestCapture records the last request's Authorization header and query.
type requestCapture struct {
	body  []byte
	auth  string
	query string
}

func (rc *requestCapture) RoundTrip(r *http.Request) (*http.Response, error) {
	rc.auth, rc.query = r.Header.Get("Authorization"), r.URL.RawQuery
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(rc.body)), Header: http.Header{}}, nil
}

// failingTransport fails every request the way a dropped connection does.
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection reset by peer")
}

// #74: the key travels in the Authorization header, never in the URL, so
// it cannot show up in a logged error.
func TestFetchStock_KeyInHeaderNotURL(t *testing.T) {
	fixture, err := os.ReadFile("testdata/twelvedata_quote_aapl.json")
	require.NoError(t, err)
	rc := &requestCapture{body: fixture}
	f := NewAssetFetcher("secret-key-123")
	f.SetHTTPClient(&http.Client{Transport: rc})

	_, err = f.FetchStock("AAPL")

	require.NoError(t, err)
	assert.Equal(t, "apikey secret-key-123", rc.auth)
	assert.Equal(t, "symbol=AAPL", rc.query)
}

func TestFetchStock_NetworkErrorDoesNotLeakTheKey(t *testing.T) {
	f := NewAssetFetcher("secret-key-123")
	f.SetHTTPClient(&http.Client{Transport: failingTransport{}})

	_, err := f.FetchStock("AAPL")

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-key-123")
}
