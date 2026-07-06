// Package utils holds small, dependency-free helpers shared across
// WatchTower's HTTP-calling packages.
package utils

import (
	"net/http"
	"time"
)

const (
	defaultHTTPTimeout           = 30 * time.Second
	defaultTLSHandshakeTimeout   = 10 * time.Second
	defaultResponseHeaderTimeout = 15 * time.Second
)

// NewHTTPClient returns the hardened http.Client every outbound HTTP call in
// this codebase should use: an overall request timeout, plus explicit TLS
// handshake and response-header timeouts on the transport so a slow or
// hanging remote (a stalled TLS negotiation, or a server that accepts the
// connection but never starts sending headers) fails fast with a clear
// signal rather than only being caught by the coarser top-level Timeout.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultHTTPTimeout,
		Transport: &http.Transport{
			TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
			ResponseHeaderTimeout: defaultResponseHeaderTimeout,
			DisableKeepAlives:     false,
		},
	}
}
