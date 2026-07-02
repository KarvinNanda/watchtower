package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/middleware"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newTestRouter builds a minimal Gin engine with mw as the only middleware,
// backing a single GET/POST "/probe" route that just replies 200.
func newTestRouter(mw gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	r.Use(mw)
	r.GET("/probe/:id", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.POST("/probe", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return r
}

func doGet(t *testing.T, r *gin.Engine, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = "198.51.100.7:54321"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestRateLimiter_AllowUnderLimit(t *testing.T) {
	t.Parallel()
	r := newTestRouter(middleware.RateLimiter(10, 60))

	for i := 1; i <= 9; i++ {
		rec := doGet(t, r, "/probe/x")
		assert.Equalf(t, http.StatusOK, rec.Code, "request %d of 9 should be allowed under a limit of 10", i)
	}
}

func TestRateLimiter_BlockOverLimit(t *testing.T) {
	t.Parallel()
	r := newTestRouter(middleware.RateLimiter(10, 60))

	for i := 1; i <= 10; i++ {
		rec := doGet(t, r, "/probe/x")
		assert.Equalf(t, http.StatusOK, rec.Code, "request %d of 10 should be allowed", i)
	}

	rec := doGet(t, r, "/probe/x")
	require.Equal(t, http.StatusTooManyRequests, rec.Code, "the 11th request must be rejected")
	assert.JSONEq(t, `{"success":false,"error":"rate_limit_exceeded","message":"too many requests"}`, rec.Body.String())
}

func TestRateLimiter_ResetAfterWindow(t *testing.T) {
	t.Parallel()
	// A short 1-second window keeps this test fast while still exercising
	// the real cleanup-goroutine reset behavior.
	r := newTestRouter(middleware.RateLimiter(10, 1))

	for i := 1; i <= 10; i++ {
		rec := doGet(t, r, "/probe/x")
		require.Equalf(t, http.StatusOK, rec.Code, "request %d of 10 should be allowed", i)
	}
	rec := doGet(t, r, "/probe/x")
	require.Equal(t, http.StatusTooManyRequests, rec.Code, "the 11th request within the window must be rejected")

	time.Sleep(1200 * time.Millisecond)

	rec = doGet(t, r, "/probe/x")
	assert.Equal(t, http.StatusOK, rec.Code, "a request after the window resets must be allowed again")
}

// queryTarget builds a properly-encoded "/probe/x?key=value" URL so that
// raw SQLi payloads (quotes, spaces, semicolons) don't break HTTP request
// parsing itself before the middleware even runs.
func queryTarget(key, value string) string {
	v := url.Values{}
	v.Set(key, value)
	return "/probe/x?" + v.Encode()
}

func TestSQLInjectionGuard_Detect(t *testing.T) {
	t.Parallel()
	r := newTestRouter(middleware.SQLInjectionGuard())

	blocked := []struct{ key, value string }{
		{"id", "1' OR '1'='1"},
		{"name", "'; DROP TABLE users--"},
		{"q", "UNION SELECT * FROM users"},
		{"x", "1; EXEC xp_cmdshell('dir')"},
	}
	for _, tc := range blocked {
		target := queryTarget(tc.key, tc.value)
		rec := doGet(t, r, target)
		assert.Equalf(t, http.StatusBadRequest, rec.Code, "expected %s=%q to be blocked", tc.key, tc.value)
	}

	allowed := []struct{ key, value string }{
		{"name", "john"},
		{"symbol", "NVDA"},
		{"keyword", "android"},
	}
	for _, tc := range allowed {
		target := queryTarget(tc.key, tc.value)
		rec := doGet(t, r, target)
		assert.Equalf(t, http.StatusOK, rec.Code, "expected %s=%q to be allowed", tc.key, tc.value)
	}
}

func TestSecurityHeaders_Present(t *testing.T) {
	t.Parallel()
	r := newTestRouter(middleware.SecurityHeaders())

	rec := doGet(t, r, "/probe/x")
	require.Equal(t, http.StatusOK, rec.Code)

	headers := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"X-Xss-Protection":       "1; mode=block",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for name, want := range headers {
		assert.Equal(t, want, rec.Header().Get(name), "header %s", name)
	}
}

func TestRequestSizeLimit_Reject(t *testing.T) {
	t.Parallel()
	r := newTestRouter(middleware.RequestSizeLimit(1 << 20))

	oversized := strings.NewReader(strings.Repeat("x", (1<<20)+1))
	req := httptest.NewRequest(http.MethodPost, "/probe", oversized)
	req.ContentLength = int64(oversized.Len())
	req.RemoteAddr = "198.51.100.7:54321"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}
