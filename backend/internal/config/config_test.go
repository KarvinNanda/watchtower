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

func TestLoad_TrustedProxiesDefaultsEmpty(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	cfg, err := Load("does-not-exist.env")
	require.NoError(t, err)
	assert.Empty(t, cfg.Server.TrustedProxies, "no proxy is trusted unless configured")
}

func TestLoad_TrustedProxiesFromEnv(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	t.Setenv("TRUSTED_PROXIES", " 10.0.0.0/8, 172.16.0.0/12,,192.168.0.0/16 ")
	cfg, err := Load("does-not-exist.env")
	require.NoError(t, err)
	assert.Equal(t, []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}, cfg.Server.TrustedProxies)
}

// docker-compose.yml passes every setting as ${VAR:-}, so an unset value
// arrives as an empty string. It must fall back to configs/config.yaml.
func TestLoad_EmptyEnvFallsBackToDefault(t *testing.T) {
	t.Chdir("../..") // backend/, where configs/config.yaml lives
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	t.Setenv("ASSET_SCHEDULER_INTERVAL_HOURS", "")
	cfg, err := Load("does-not-exist.env")
	require.NoError(t, err)
	assert.Equal(t, 4, cfg.Scheduler.AssetIntervalHours)
}

func TestLoad_TimezoneDefaultsToJakarta(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	cfg, err := Load("does-not-exist.env")
	require.NoError(t, err)
	assert.Equal(t, "Asia/Jakarta", cfg.Scheduler.Timezone.String())
}

// docker-compose.yml passes ${APP_TIMEZONE:-}: unset arrives as "". That
// must mean the default, not UTC (time.LoadLocation("") returns UTC).
func TestLoad_EmptyTimezoneMeansDefault(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	t.Setenv("APP_TIMEZONE", "")
	cfg, err := Load("does-not-exist.env")
	require.NoError(t, err)
	assert.Equal(t, "Asia/Jakarta", cfg.Scheduler.Timezone.String())
}

func TestLoad_TimezoneFromEnv(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	t.Setenv("APP_TIMEZONE", "Europe/Berlin")
	cfg, err := Load("does-not-exist.env")
	require.NoError(t, err)
	assert.Equal(t, "Europe/Berlin", cfg.Scheduler.Timezone.String())
}

func TestLoad_InvalidTimezoneFails(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	t.Setenv("APP_TIMEZONE", "Mars/Olympus_Mons")
	_, err := Load("does-not-exist.env")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "APP_TIMEZONE")
}
