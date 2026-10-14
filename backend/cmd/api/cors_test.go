package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAllowedOrigins_DevOnlyWhenUnset(t *testing.T) {
	assert.Equal(t, []string{"http://localhost:5173"}, allowedOrigins(""))
}

func TestAllowedOrigins_AddsFrontendURL(t *testing.T) {
	assert.Equal(t,
		[]string{"http://localhost:5173", "http://localhost:8080"},
		allowedOrigins("http://localhost:8080"))
}
