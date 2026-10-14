package main

import (
	"testing"

	"github.com/gin-contrib/cors"
	"github.com/stretchr/testify/assert"
)

func TestCORSConfig_ProductionWithoutFrontendURLDoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() { cors.New(corsConfig("", true)) })
}
