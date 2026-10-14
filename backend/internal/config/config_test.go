package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_CookieSecureDefaultsFalse(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	cfg, err := Load("does-not-exist.env")
	require.NoError(t, err)
	assert.False(t, cfg.Server.CookieSecure)
}

func TestLoad_CookieSecureFromEnv(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	t.Setenv("COOKIE_SECURE", "true")
	cfg, err := Load("does-not-exist.env")
	require.NoError(t, err)
	assert.True(t, cfg.Server.CookieSecure)
}
