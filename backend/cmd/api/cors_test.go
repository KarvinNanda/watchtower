package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAllowedOrigins_DevOnlyWhenUnset(t *testing.T) {
	assert.Equal(t, []string{"http://localhost:5173"}, allowedOrigins("", false))
}

func TestAllowedOrigins_AddsFrontendURL(t *testing.T) {
	assert.Equal(t,
		[]string{"http://localhost:5173", "http://localhost:8080"},
		allowedOrigins("http://localhost:8080", false))
}

func TestAllowedOrigins_ProductionDropsViteDevOrigin(t *testing.T) {
	// localhost:5173 is the same site as localhost:8080, so any page served
	// on Vite's default port would get credentialed access in production.
	assert.Equal(t, []string{"http://localhost:8080"}, allowedOrigins("http://localhost:8080", true))
}
