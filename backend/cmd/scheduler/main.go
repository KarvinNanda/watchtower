// Command scheduler runs the WatchTower background jobs: the asset price
// alert pipeline and the sentinel threat-intel pipeline, each on its own
// configurable interval, with graceful shutdown on SIGINT/SIGTERM.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/karvin-nanda/watchtower/internal/asset"
	"github.com/karvin-nanda/watchtower/internal/config"
	"github.com/karvin-nanda/watchtower/internal/currency"
	"github.com/karvin-nanda/watchtower/internal/db"
	"github.com/karvin-nanda/watchtower/internal/notifier"
	"github.com/karvin-nanda/watchtower/internal/scheduler"
	"github.com/karvin-nanda/watchtower/internal/sentinel"
)

func main() {
	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("[ERROR] main: load config: %v", err)
	}

	database, err := db.New(cfg)
	if err != nil {
		log.Fatalf("[ERROR] main: connect to database: %v", err)
	}
	defer database.Close()

	if err := database.RunMigrations("migrations"); err != nil {
		log.Fatalf("[ERROR] main: run migrations: %v", err)
	}

	if cfg.ExternalAPI.CoinGeckoBaseURL != "" {
		asset.CoinGeckoBaseURL = cfg.ExternalAPI.CoinGeckoBaseURL
	}
	if cfg.ExternalAPI.OpenERBaseURL != "" {
		currency.BaseURL = cfg.ExternalAPI.OpenERBaseURL
	}

	notif := notifier.NewNotifier(cfg.Telegram.AssetBotToken, cfg.Telegram.SentinelBotToken)
	assetFetcher := asset.NewAssetFetcher(cfg.ExternalAPI.TwelveDataAPIKey)
	deepSeekAnalyzer := asset.NewDeepSeekAnalyzer(cfg.DeepSeek.APIKey, cfg.DeepSeek.Model)
	sentinelFetcher := sentinel.NewSentinelFetcher(cfg.ExternalAPI.GitHubToken)
	sentinelAnalyzer := sentinel.NewSentinelAnalyzer(cfg.DeepSeek.APIKey, cfg.DeepSeek.Model)

	assetInterval := time.Duration(cfg.Scheduler.AssetIntervalHours) * time.Hour
	sentinelInterval := time.Duration(cfg.Scheduler.SentinelIntervalHours) * time.Hour

	assetWorker := scheduler.NewAssetWorker(
		database.SQL,
		database.Redis,
		assetFetcher,
		notif,
		deepSeekAnalyzer,
		cfg.Scheduler.AssetIntervalHours,
		cfg.Limits.MaxUniqueSymbols,
		cfg.Limits.AlertPriceMovePct,
		assetInterval,
	)

	sentinelWorker := scheduler.NewSentinelWorker(
		database.SQL,
		sentinelFetcher,
		sentinelAnalyzer,
		notif,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		assetWorker.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		sentinelWorker.Start(ctx, sentinelInterval)
	}()

	<-ctx.Done()
	log.Println("[INFO] main: shutdown signal received, waiting for workers to finish current run")

	wg.Wait()
	log.Println("[INFO] main: scheduler stopped gracefully")
}
