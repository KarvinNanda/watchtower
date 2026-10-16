// Package config loads WatchTower configuration from environment variables
// (optionally sourced from a .env file) with defaults supplied by
// configs/config.yaml.
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// defaultTimezone is used when APP_TIMEZONE is unset or empty.
const defaultTimezone = "Asia/Jakarta"

type ServerConfig struct {
	Env  string
	Port int
	// FrontendURL is added to the CORS allow-list (in addition to the Vite
	// dev origin).
	FrontendURL string
	// CookieSecure sets the auth cookie's Secure flag (and SameSite=Strict).
	// Keep false for self-hosting over http://localhost; set true only
	// behind HTTPS.
	CookieSecure bool
	// TrustedProxies lists the IPs/CIDRs allowed to set X-Forwarded-For.
	// Empty means none: the client IP is the TCP peer, so a client cannot
	// pick its own rate-limit key with a forged header.
	TrustedProxies []string
}

type DatabaseConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Name     string
}

type RedisConfig struct {
	Host     string
	Port     int
	Password string
}

type JWTConfig struct {
	Secret      string
	ExpiryHours int
}

type TelegramConfig struct {
	AssetBotToken    string
	SentinelBotToken string
	// Mode is "polling" (development, default) or "webhook" (production).
	Mode string
}

type ExternalAPIConfig struct {
	TwelveDataAPIKey string
	CoinGeckoBaseURL string
	// CoinGeckoAPIKey is an optional Demo API key (#65); empty uses the
	// keyless public API.
	CoinGeckoAPIKey string
	OpenERBaseURL   string
	GitHubToken     string
}

type SchedulerConfig struct {
	AssetIntervalHours    int
	SentinelIntervalHours int
	// Timezone is used for timestamps in Telegram messages (APP_TIMEZONE).
	Timezone *time.Location
}

type LimitsConfig struct {
	MaxUniqueSymbols   int
	AlertCooldownHours int
	AlertPriceMovePct  float64
	// TwelveDataManualDailyCredits is the instance's daily Twelve Data
	// allowance for manual "Check now" refreshes; 0 disables them.
	TwelveDataManualDailyCredits int
}

// Config aggregates every configurable value used across the WatchTower
// binaries (API server and scheduler).
type Config struct {
	Server      ServerConfig
	Database    DatabaseConfig
	Redis       RedisConfig
	JWT         JWTConfig
	Telegram    TelegramConfig
	ExternalAPI ExternalAPIConfig
	Scheduler   SchedulerConfig
	Limits      LimitsConfig
}

// envBindings maps the flat viper key used internally to the environment
// variable name documented in .env.example. Keeping this list explicit
// avoids relying on viper's automatic dot-to-underscore key translation,
// which does not match WatchTower's flat env var naming.
// #nosec G101 -- these are env var *names* (e.g. "JWT_SECRET"), not credential
// values; the actual secrets are read from the environment at runtime.
var envBindings = map[string]string{
	"app_env":                           "APP_ENV",
	"app_port":                          "APP_PORT",
	"app_timezone":                      "APP_TIMEZONE",
	"frontend_url":                      "FRONTEND_URL",
	"cookie_secure":                     "COOKIE_SECURE",
	"trusted_proxies":                   "TRUSTED_PROXIES",
	"db_host":                           "DB_HOST",
	"db_port":                           "DB_PORT",
	"db_user":                           "DB_USER",
	"db_password":                       "DB_PASSWORD",
	"db_name":                           "DB_NAME",
	"redis_host":                        "REDIS_HOST",
	"redis_port":                        "REDIS_PORT",
	"redis_password":                    "REDIS_PASSWORD",
	"jwt_secret":                        "JWT_SECRET",
	"jwt_expiry_hours":                  "JWT_EXPIRY_HOURS",
	"telegram_asset_bot_token":          "TELEGRAM_ASSET_BOT_TOKEN",
	"telegram_sentinel_bot_token":       "TELEGRAM_SENTINEL_BOT_TOKEN",
	"telegram_mode":                     "TELEGRAM_MODE",
	"twelve_data_api_key":               "TWELVE_DATA_API_KEY",
	"coingecko_base_url":                "COINGECKO_BASE_URL",
	"coingecko_api_key":                 "COINGECKO_API_KEY",
	"open_er_base_url":                  "OPEN_ER_BASE_URL",
	"github_token":                      "GITHUB_TOKEN",
	"asset_scheduler_interval_hours":    "ASSET_SCHEDULER_INTERVAL_HOURS",
	"sentinel_scheduler_interval_hours": "SENTINEL_SCHEDULER_INTERVAL_HOURS",
	"max_unique_symbols":                "MAX_UNIQUE_SYMBOLS",
	"alert_cooldown_hours":              "ALERT_COOLDOWN_HOURS",
	"alert_price_move_pct":              "ALERT_PRICE_MOVE_PCT",
	"twelve_data_manual_daily_credits":  "TWELVE_DATA_MANUAL_DAILY_CREDITS",
}

// Load reads configuration in the following precedence (highest first):
// real process environment variables, values from the .env file at envPath
// (if it exists), then defaults declared in configs/config.yaml.
func Load(envPath string) (*Config, error) {
	if envPath == "" {
		envPath = ".env"
	}
	// .env is optional: in production real environment variables are
	// injected by the platform and no file is present.
	_ = godotenv.Load(envPath)

	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath("configs")
	v.AddConfigPath("./configs")
	v.AddConfigPath(".")

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("config: read config.yaml: %w", err)
		}
	}

	for key, env := range envBindings {
		if err := v.BindEnv(key, env); err != nil {
			return nil, fmt.Errorf("config: bind env %s: %w", env, err)
		}
	}

	num := &strictReader{v: v}
	cfg := &Config{
		Server: ServerConfig{
			Env:            v.GetString("app_env"),
			Port:           num.intVal("app_port"),
			FrontendURL:    v.GetString("frontend_url"),
			CookieSecure:   num.boolVal("cookie_secure"),
			TrustedProxies: splitList(v.GetString("trusted_proxies")),
		},
		Database: DatabaseConfig{
			Host:     v.GetString("db_host"),
			Port:     num.intVal("db_port"),
			User:     v.GetString("db_user"),
			Password: v.GetString("db_password"),
			Name:     v.GetString("db_name"),
		},
		Redis: RedisConfig{
			Host:     v.GetString("redis_host"),
			Port:     num.intVal("redis_port"),
			Password: v.GetString("redis_password"),
		},
		JWT: JWTConfig{
			Secret:      v.GetString("jwt_secret"),
			ExpiryHours: num.intVal("jwt_expiry_hours"),
		},
		Telegram: TelegramConfig{
			AssetBotToken:    v.GetString("telegram_asset_bot_token"),
			SentinelBotToken: v.GetString("telegram_sentinel_bot_token"),
			Mode:             telegramMode(v.GetString("telegram_mode")),
		},
		ExternalAPI: ExternalAPIConfig{
			TwelveDataAPIKey: v.GetString("twelve_data_api_key"),
			CoinGeckoBaseURL: v.GetString("coingecko_base_url"),
			CoinGeckoAPIKey:  v.GetString("coingecko_api_key"),
			OpenERBaseURL:    v.GetString("open_er_base_url"),
			GitHubToken:      v.GetString("github_token"),
		},
		Scheduler: SchedulerConfig{
			AssetIntervalHours:    num.intVal("asset_scheduler_interval_hours"),
			SentinelIntervalHours: num.intVal("sentinel_scheduler_interval_hours"),
		},
		Limits: LimitsConfig{
			MaxUniqueSymbols:             num.intVal("max_unique_symbols"),
			AlertCooldownHours:           num.intVal("alert_cooldown_hours"),
			AlertPriceMovePct:            num.floatVal("alert_price_move_pct"),
			TwelveDataManualDailyCredits: num.intVal("twelve_data_manual_daily_credits"),
		},
	}

	if len(num.errs) > 0 {
		return nil, fmt.Errorf("config: %s", strings.Join(num.errs, "; "))
	}
	if cfg.Database.Host == "" {
		return nil, fmt.Errorf("config: DB_HOST is required")
	}
	if cfg.JWT.Secret == "" && cfg.Server.Env != "development" {
		return nil, fmt.Errorf("config: JWT_SECRET is required outside development")
	}
	if cfg.Limits.AlertCooldownHours < 0 {
		return nil, fmt.Errorf("config: ALERT_COOLDOWN_HOURS must be 0 or more, got %d", cfg.Limits.AlertCooldownHours)
	}
	if cfg.Limits.TwelveDataManualDailyCredits < 0 {
		return nil, fmt.Errorf("config: TWELVE_DATA_MANUAL_DAILY_CREDITS must be 0 or more, got %d", cfg.Limits.TwelveDataManualDailyCredits)
	}

	tzName := v.GetString("app_timezone")
	if tzName == "" {
		tzName = defaultTimezone
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return nil, fmt.Errorf("config: APP_TIMEZONE %q is not a valid IANA timezone: %w", tzName, err)
	}
	cfg.Scheduler.Timezone = loc

	return cfg, nil
}

// strictReader reads numeric and boolean settings strictly. viper's GetInt,
// GetFloat64 and GetBool turn a typo like "abc" into 0 or false without an
// error (#50); here every bad value is collected and named by its env var.
// An unset value keeps viper's meaning (the config.yaml default, else 0).
type strictReader struct {
	v    *viper.Viper
	errs []string
}

// raw returns the trimmed value and whether it was set at all. A value of
// only spaces is set (viper returns it), so it is parsed and fails.
func (r *strictReader) raw(key string) (string, bool) {
	v := r.v.GetString(key)
	return strings.TrimSpace(v), v != ""
}

func (r *strictReader) fail(key, want, got string) {
	r.errs = append(r.errs, fmt.Sprintf("%s must be %s, got %q", envBindings[key], want, got))
}

func (r *strictReader) intVal(key string) int {
	s, set := r.raw(key)
	if !set {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		r.fail(key, "a whole number", s)
	}
	return n
}

func (r *strictReader) floatVal(key string) float64 {
	s, set := r.raw(key)
	if !set {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		r.fail(key, "a number", s)
	}
	return f
}

func (r *strictReader) boolVal(key string) bool {
	s, set := r.raw(key)
	if !set {
		return false
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		r.fail(key, "true or false", s)
	}
	return b
}

// DSN builds a MySQL data source name compatible with
// github.com/go-sql-driver/mysql.
func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci&loc=UTC",
		d.User, d.Password, d.Host, d.Port, d.Name)
}

// Addr builds a Redis "host:port" address string.
func (r RedisConfig) Addr() string {
	return fmt.Sprintf("%s:%d", r.Host, r.Port)
}

// telegramMode normalizes TELEGRAM_MODE, defaulting to "polling"
// (development) when unset. Any value other than "webhook" is treated as
// polling mode.
func telegramMode(mode string) string {
	if mode == "webhook" {
		return "webhook"
	}
	return "polling"
}

// splitList parses a comma-separated env value, dropping blanks.
func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
