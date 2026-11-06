package sentinel

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEPSS(t *testing.T) {
	body, err := os.ReadFile("testdata/epss.json")
	require.NoError(t, err)

	got, err := parseEPSS(body)
	require.NoError(t, err)
	assert.Len(t, got, 3)
	assert.InDelta(t, 0.99999, got["CVE-2024-3400"], 1e-9)
	assert.InDelta(t, 0.04753, got["CVE-1999-0001"], 1e-9)
}

func TestParseEPSS_SkipsUnparseable(t *testing.T) {
	got, err := parseEPSS([]byte(`{"data":[{"cve":"CVE-2024-0001","epss":"abc"},{"cve":"CVE-2024-0002","epss":"0.5"}]}`))
	require.NoError(t, err)
	assert.Equal(t, map[string]float64{"CVE-2024-0002": 0.5}, got)
}

func TestEPSSChunks(t *testing.T) {
	var ids []string
	for i := 0; i < 250; i++ {
		ids = append(ids, fmt.Sprintf("CVE-2024-%04d", 1000+i))
	}
	ids = append(ids, "CVE-1-1", "x;DROP", ids[0])

	chunks := epssChunks(ids)
	require.Len(t, chunks, 3)
	assert.Len(t, chunks[0], 100)
	assert.Len(t, chunks[1], 100)
	assert.Len(t, chunks[2], 50)
	for _, c := range chunks {
		for _, id := range c {
			assert.Regexp(t, cveIDPattern, id)
		}
	}
}

// pointAt redirects a package URL var to a test server and restores it.
func pointAt(t *testing.T, target *string, value string) {
	t.Helper()
	old := *target
	*target = value
	t.Cleanup(func() { *target = old })
}

func TestFetchEPSS_PartialFailure(t *testing.T) {
	fixture, err := os.ReadFile("testdata/epss.json")
	require.NoError(t, err)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Write(fixture)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	pointAt(t, &epssAPIURL, srv.URL)

	var ids []string
	for i := 0; i < 150; i++ {
		ids = append(ids, fmt.Sprintf("CVE-2024-%04d", 1000+i))
	}

	f := NewSentinelFetcher("")
	got, err := f.FetchEPSS(ids)
	require.Error(t, err)
	assert.Len(t, got, 3)
	assert.InDelta(t, 0.99999, got["CVE-2024-3400"], 1e-9)
	assert.EqualValues(t, 2, atomic.LoadInt32(&hits))
}

func TestFetchNVDByCVE_InvalidIDNoRequest(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer srv.Close()
	pointAt(t, &nvdAPIURL, srv.URL)

	f := NewSentinelFetcher("")
	item, err := f.FetchNVDByCVE("CVE-2024-3400&x=1")
	require.Error(t, err)
	assert.Nil(t, item)
	assert.EqualValues(t, 0, atomic.LoadInt32(&hits))
}

func TestFetchNVDByCVE_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "CVE-2024-3400", r.URL.Query().Get("cveId"))
		w.Write([]byte(`{"totalResults":0,"vulnerabilities":[]}`))
	}))
	defer srv.Close()
	pointAt(t, &nvdAPIURL, srv.URL)
	old := nvdRateLimitDelay
	nvdRateLimitDelay = 0
	t.Cleanup(func() { nvdRateLimitDelay = old })

	f := NewSentinelFetcher("")
	item, err := f.FetchNVDByCVE("CVE-2024-3400")
	require.NoError(t, err)
	assert.Nil(t, item)
}
