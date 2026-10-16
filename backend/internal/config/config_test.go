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

func TestLoad_ManualCreditsDefault(t *testing.T) {
	t.Chdir("../..") // backend/, where configs/config.yaml lives
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	cfg, err := Load("does-not-exist.env")
	require.NoError(t, err)
	assert.Equal(t, 400, cfg.Limits.TwelveDataManualDailyCredits)
}

func TestLoad_ManualCreditsFromEnvAndZero(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	t.Setenv("TWELVE_DATA_MANUAL_DAILY_CREDITS", "0")
	cfg, err := Load("does-not-exist.env")
	require.NoError(t, err)
	assert.Equal(t, 0, cfg.Limits.TwelveDataManualDailyCredits, "0 disables manual stock refreshes")
}

func TestLoad_NegativeManualCreditsFails(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	t.Setenv("TWELVE_DATA_MANUAL_DAILY_CREDITS", "-1")
	_, err := Load("does-not-exist.env")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TWELVE_DATA_MANUAL_DAILY_CREDITS")
}

// #50: viper's GetInt turns a typo into 0 silently (e.g. manual stock
// refresh off). A setting that is not a number or a boolean stops startup
// and every bad one is named.
func TestLoad_NonNumericSettingsFail(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	t.Setenv("TWELVE_DATA_MANUAL_DAILY_CREDITS", "abc")
	t.Setenv("MAX_UNIQUE_SYMBOLS", "1.5")
	t.Setenv("ALERT_PRICE_MOVE_PCT", "3%")
	t.Setenv("COOKIE_SECURE", "maybe")

	_, err := Load("does-not-exist.env")

	require.Error(t, err)
	for _, name := range []string{"TWELVE_DATA_MANUAL_DAILY_CREDITS", "MAX_UNIQUE_SYMBOLS", "ALERT_PRICE_MOVE_PCT", "COOKIE_SECURE"} {
		assert.Contains(t, err.Error(), name)
	}
}

func TestLoad_NumericSettingsWithSpacesAreRead(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	t.Setenv("MAX_UNIQUE_SYMBOLS", " 50 ")
	t.Setenv("ALERT_PRICE_MOVE_PCT", "2.5")
	t.Setenv("COOKIE_SECURE", "true")

	cfg, err := Load("does-not-exist.env")

	require.NoError(t, err)
	assert.Equal(t, 50, cfg.Limits.MaxUniqueSymbols)
	assert.Equal(t, 2.5, cfg.Limits.AlertPriceMovePct)
	assert.True(t, cfg.Server.CookieSecure)
}

// Review of #50: a value of only spaces is not "unset"; viper returns it, so
// it must fail like any other non-number instead of silently becoming 0.
func TestLoad_WhitespaceOnlySettingFails(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	t.Setenv("MAX_UNIQUE_SYMBOLS", "   ")

	_, err := Load("does-not-exist.env")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "MAX_UNIQUE_SYMBOLS")
}

// #65: the optional CoinGecko Demo key comes from the environment.
func TestLoad_CoinGeckoAPIKey(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	cfg, err := Load("does-not-exist.env")
	require.NoError(t, err)
	assert.Empty(t, cfg.ExternalAPI.CoinGeckoAPIKey, "unset by default")

	t.Setenv("COINGECKO_API_KEY", "demo-key-123")
	cfg, err = Load("does-not-exist.env")
	require.NoError(t, err)
	assert.Equal(t, "demo-key-123", cfg.ExternalAPI.CoinGeckoAPIKey)
}

// #75: the default cooldown for new users cannot be negative.
func TestLoad_NegativeAlertCooldownFails(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	t.Setenv("ALERT_COOLDOWN_HOURS", "-1")
	_, err := Load("does-not-exist.env")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ALERT_COOLDOWN_HOURS")
}

// #78: the default cooldown follows the same 0 to 168 hours range.
func TestLoad_AlertCooldownOverAWeekFails(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("APP_ENV", "development")
	t.Setenv("ALERT_COOLDOWN_HOURS", "169")
	_, err := Load("does-not-exist.env")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ALERT_COOLDOWN_HOURS")
}
