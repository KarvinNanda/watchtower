// Package config loads WatchTower configuration from environment variables
// (optionally sourced from a .env file) with defaults supplied by
// configs/config.yaml.
package config

import (
	"fmt"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

type ServerConfig struct {
	Env  string
	Port int
	// FrontendURL is the only origin allowed by CORSMiddleware when
	// Env == "production". Ignored in development, where all origins are
	// allowed.
	FrontendURL string
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

type DeepSeekConfig struct {
	APIKey string
	Model  string
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
	OpenERBaseURL    string
	GitHubToken      string
}

type SchedulerConfig struct {
	AssetIntervalHours    int
	SentinelIntervalHours int
}

type LimitsConfig struct {
	MaxUniqueSymbols   int
	AlertCooldownHours int
	AlertPriceMovePct  float64
}

// Config aggregates every configurable value used across the WatchTower
// binaries (API server and scheduler).
type Config struct {
	Server      ServerConfig
	Database    DatabaseConfig
	Redis       RedisConfig
	JWT         JWTConfig
	DeepSeek    DeepSeekConfig
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
	"frontend_url":                      "FRONTEND_URL",
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
	"deepseek_api_key":                  "DEEPSEEK_API_KEY",
	"deepseek_model":                    "DEEPSEEK_MODEL",
	"telegram_asset_bot_token":          "TELEGRAM_ASSET_BOT_TOKEN",
	"telegram_sentinel_bot_token":       "TELEGRAM_SENTINEL_BOT_TOKEN",
	"telegram_mode":                     "TELEGRAM_MODE",
	"twelve_data_api_key":               "TWELVE_DATA_API_KEY",
	"coingecko_base_url":                "COINGECKO_BASE_URL",
	"open_er_base_url":                  "OPEN_ER_BASE_URL",
	"github_token":                      "GITHUB_TOKEN",
	"asset_scheduler_interval_hours":    "ASSET_SCHEDULER_INTERVAL_HOURS",
	"sentinel_scheduler_interval_hours": "SENTINEL_SCHEDULER_INTERVAL_HOURS",
	"max_unique_symbols":                "MAX_UNIQUE_SYMBOLS",
	"alert_cooldown_hours":              "ALERT_COOLDOWN_HOURS",
	"alert_price_move_pct":              "ALERT_PRICE_MOVE_PCT",
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

	cfg := &Config{
		Server: ServerConfig{
			Env:         v.GetString("app_env"),
			Port:        v.GetInt("app_port"),
			FrontendURL: v.GetString("frontend_url"),
		},
		Database: DatabaseConfig{
			Host:     v.GetString("db_host"),
			Port:     v.GetInt("db_port"),
			User:     v.GetString("db_user"),
			Password: v.GetString("db_password"),
			Name:     v.GetString("db_name"),
		},
		Redis: RedisConfig{
			Host:     v.GetString("redis_host"),
			Port:     v.GetInt("redis_port"),
			Password: v.GetString("redis_password"),
		},
		JWT: JWTConfig{
			Secret:      v.GetString("jwt_secret"),
			ExpiryHours: v.GetInt("jwt_expiry_hours"),
		},
		DeepSeek: DeepSeekConfig{
			APIKey: v.GetString("deepseek_api_key"),
			Model:  v.GetString("deepseek_model"),
		},
		Telegram: TelegramConfig{
			AssetBotToken:    v.GetString("telegram_asset_bot_token"),
			SentinelBotToken: v.GetString("telegram_sentinel_bot_token"),
			Mode:             telegramMode(v.GetString("telegram_mode")),
		},
		ExternalAPI: ExternalAPIConfig{
			TwelveDataAPIKey: v.GetString("twelve_data_api_key"),
			CoinGeckoBaseURL: v.GetString("coingecko_base_url"),
			OpenERBaseURL:    v.GetString("open_er_base_url"),
			GitHubToken:      v.GetString("github_token"),
		},
		Scheduler: SchedulerConfig{
			AssetIntervalHours:    v.GetInt("asset_scheduler_interval_hours"),
			SentinelIntervalHours: v.GetInt("sentinel_scheduler_interval_hours"),
		},
		Limits: LimitsConfig{
			MaxUniqueSymbols:   v.GetInt("max_unique_symbols"),
			AlertCooldownHours: v.GetInt("alert_cooldown_hours"),
			AlertPriceMovePct:  v.GetFloat64("alert_price_move_pct"),
		},
	}

	if cfg.Database.Host == "" {
		return nil, fmt.Errorf("config: DB_HOST is required")
	}
	if cfg.JWT.Secret == "" && cfg.Server.Env != "development" {
		return nil, fmt.Errorf("config: JWT_SECRET is required outside development")
	}

	return cfg, nil
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
