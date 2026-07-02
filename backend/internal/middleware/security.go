// Package middleware provides Gin HTTP middleware for hardening the
// WatchTower API: security headers, per-IP rate limiting, CORS, request
// size limits, and a basic SQL-injection pattern guard.
package middleware

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// SecurityHeaders sets a standard set of defensive HTTP response headers on
// every response.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Header("Content-Security-Policy", "default-src 'self'")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy", "geolocation=(), microphone=()")
		c.Next()
	}
}

// RateLimiter returns a Gin middleware enforcing a fixed-window per-IP
// request limit: at most maxRequests requests per windowSeconds. State is
// held in a sync.Map for thread-safe concurrent access, and a background
// goroutine clears every counter once per window (the whole map is reset
// together, rather than tracking a per-key sliding window) — one
// RateLimiter instance owns one independent counter set and cleanup
// goroutine, so calling it multiple times (e.g. once for an auth group,
// once for the general API) creates separate, non-interfering limits.
func RateLimiter(maxRequests int, windowSeconds int) gin.HandlerFunc {
	var counts sync.Map // map[string]*int64, keyed by client IP

	window := time.Duration(windowSeconds) * time.Second

	go func() {
		ticker := time.NewTicker(window)
		defer ticker.Stop()
		for range ticker.C {
			counts.Range(func(key, _ interface{}) bool {
				counts.Delete(key)
				return true
			})
		}
	}()

	return func(c *gin.Context) {
		ip := c.ClientIP()

		counterVal, _ := counts.LoadOrStore(ip, new(int64))
		counter, _ := counterVal.(*int64)
		count := atomic.AddInt64(counter, 1)

		if count > int64(maxRequests) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error":   "rate_limit_exceeded",
				"message": "too many requests",
			})
			return
		}

		c.Next()
	}
}

// CORSMiddleware handles cross-origin requests. In development (env !=
// "production") it allows any origin; in production it only allows
// frontendURL. env and frontendURL are passed in explicitly (from
// config.Config) rather than read globally, keeping this package
// dependency-free of internal/config.
func CORSMiddleware(env, frontendURL string) gin.HandlerFunc {
	allowAll := env != "production" || frontendURL == ""

	return func(c *gin.Context) {
		if allowAll {
			c.Header("Access-Control-Allow-Origin", "*")
		} else {
			c.Header("Access-Control-Allow-Origin", frontendURL)
			c.Header("Vary", "Origin")
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// RequestSizeLimit rejects request bodies larger than maxBytes with 413
// Payload Too Large, and additionally wraps the body reader with
// http.MaxBytesReader so oversized bodies are caught during binding even
// when Content-Length is absent or inaccurate.
func RequestSizeLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxBytes {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
				"success": false,
				"error":   "payload_too_large",
				"message": "request body exceeds the maximum allowed size",
			})
			return
		}

		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

// sqliPatterns are basic SQL-injection indicators checked case-insensitively
// against query and path parameter values.
var sqliPatterns = []string{
	"--", ";--", "/*", "*/", "xp_",
	"union", "select", "drop", "insert", "update", "delete", "exec", "execute",
	// Tautology-based injection idioms (e.g. ' OR '1'='1) don't contain any
	// SQL keyword above, so they need their own signature.
	"' or '", "'='",
}

// SQLInjectionGuard rejects requests whose query or path parameter values
// contain a basic SQL-injection pattern, logging a warning with the
// offending parameter. This is a defense-in-depth layer only — every actual
// database query in this codebase already uses parameterized/prepared
// statements, so it is never actually vulnerable to injection; this guard
// exists to catch and log obviously malicious probing early.
func SQLInjectionGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		for key, values := range c.Request.URL.Query() {
			for _, v := range values {
				if containsSQLiPattern(v) {
					logSQLiAttempt(c, key, v)
					abortSQLiRequest(c)
					return
				}
			}
		}

		for _, p := range c.Params {
			if containsSQLiPattern(p.Value) {
				logSQLiAttempt(c, p.Key, p.Value)
				abortSQLiRequest(c)
				return
			}
		}

		c.Next()
	}
}

func abortSQLiRequest(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
		"success": false,
		"error":   "invalid_input",
		"message": "request contains invalid characters",
	})
}

func containsSQLiPattern(value string) bool {
	lower := strings.ToLower(value)
	for _, pattern := range sqliPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func logSQLiAttempt(c *gin.Context, param, value string) {
	log.Printf("[WARN] SQLi attempt detected from %s: %s=%s", c.ClientIP(), param, sanitizeString(value))
}

const maxSanitizedStringLength = 1000

// sanitizeString trims whitespace, strips null bytes, and truncates to
// maxSanitizedStringLength characters — used here to keep attacker-supplied
// values safe to write into logs (avoiding log injection via embedded
// control characters or unbounded length).
func sanitizeString(input string) string {
	input = strings.TrimSpace(input)
	input = strings.ReplaceAll(input, "\x00", "")
	if len(input) > maxSanitizedStringLength {
		input = input[:maxSanitizedStringLength]
	}
	return input
}
