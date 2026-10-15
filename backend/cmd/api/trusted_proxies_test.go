package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clientIPSeen runs one request from peer with a forged X-Forwarded-For and
// returns the IP gin reports, which is what the per-IP rate limiter keys on.
func clientIPSeen(t *testing.T, trusted []string, peer string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	require.NoError(t, configureTrustedProxies(r, trusted))
	var got string
	r.GET("/ip", func(c *gin.Context) { got = c.ClientIP() })

	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = peer + ":40000"
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	r.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestTrustedProxies_NoneTrustedIgnoresForwardedFor(t *testing.T) {
	assert.Equal(t, "203.0.113.9", clientIPSeen(t, nil, "203.0.113.9"),
		"a client must not choose its own rate-limit key with X-Forwarded-For")
}

func TestTrustedProxies_TrustedProxyForwardsClientIP(t *testing.T) {
	private := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	assert.Equal(t, "198.51.100.7", clientIPSeen(t, private, "172.18.0.5"), "nginx on the compose network")
	assert.Equal(t, "203.0.113.9", clientIPSeen(t, private, "203.0.113.9"), "a public peer is not a proxy")
}

func TestTrustedProxies_InvalidEntryIsAnError(t *testing.T) {
	assert.Error(t, configureTrustedProxies(gin.New(), []string{"not-an-ip"}))
}
