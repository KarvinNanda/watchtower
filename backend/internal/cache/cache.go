// Package cache layers market data across Redis (primary, TTL-bound) and
// MySQL's market_cache table (persistent fallback), per WatchTower's
// read/write flow:
//
// Read: Redis hit -> return. Redis miss -> check MySQL. MySQL fresh
// (within ttlHours) -> warm Redis, return. MySQL stale or empty -> fetch
// live from the external API, persist to both layers, return.
//
// Write (every scheduler run): fetch live, persist to Redis + MySQL
// together, TTL = ttlHours (ASSET_SCHEDULER_INTERVAL_HOURS).
package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/karvin-nanda/watchtower/internal/asset"
	"github.com/karvin-nanda/watchtower/internal/currency"
)

const marketKeyPrefix = "market:"

// MarketData is the cached market snapshot for a single asset symbol,
// mirroring the market_cache table.
type MarketData struct {
	Symbol       string    `json:"symbol"`
	PriceUSD     float64   `json:"price_usd"`
	PriceIDR     float64   `json:"price_idr"`
	ChangePct24h float64   `json:"change_pct_24h"`
	LastFetched  time.Time `json:"last_fetched"`
	Source       string    `json:"source"`
}

// Cache layers market data across Redis and MySQL's market_cache table.
type Cache struct {
	redis    *redis.Client
	db       *sql.DB
	ttlHours int
	fetcher  *asset.AssetFetcher
}

// New builds a Cache. ttlHours controls both the Redis TTL and the
// freshness window used to decide whether a MySQL row can be trusted
// without refetching from the external API (see GetMarketData) — it should
// be set to ASSET_SCHEDULER_INTERVAL_HOURS. fetcher is used to refresh a
// symbol from its external API when both cache layers are empty or stale;
// it is injected rather than constructed here so callers control exactly
// how/when its credentials (e.g. the Twelve Data API key) are supplied.
func New(redisClient *redis.Client, db *sql.DB, ttlHours int, fetcher *asset.AssetFetcher) *Cache {
	return &Cache{redis: redisClient, db: db, ttlHours: ttlHours, fetcher: fetcher}
}

func marketKey(symbol string) string {
	return marketKeyPrefix + symbol
}

// GetMarketData implements the read flow described in the package doc.
// assetType ("stock", "crypto", or "gold") is required so that, if both
// cache layers are empty or stale, GetMarketData knows how to refetch
// symbol from the correct external API.
func (c *Cache) GetMarketData(ctx context.Context, symbol, assetType string) (*MarketData, error) {
	if data, err := c.getFromRedis(ctx, symbol); err == nil {
		return data, nil
	} else if !errors.Is(err, redis.Nil) {
		log.Printf("[ERROR] GetMarketData: redis get %s: %v", symbol, err)
	}

	data, fresh, err := c.getFromMySQL(ctx, symbol)
	if err != nil {
		log.Printf("[ERROR] GetMarketData: mysql get %s: %v", symbol, err)
	}
	if data != nil && fresh {
		if err := c.setRedis(ctx, data); err != nil {
			log.Printf("[ERROR] GetMarketData: warm redis for %s: %v", symbol, err)
		}
		return data, nil
	}

	result, err := c.fetcher.FetchAsset(symbol, assetType)
	if err != nil {
		return nil, fmt.Errorf("cache: refresh %s from external api: %w", symbol, err)
	}

	priceIDR, err := currency.ConvertToIDR(result.PriceUSD)
	if err != nil {
		log.Printf("[ERROR] GetMarketData: convert to IDR for %s: %v", symbol, err)
	}

	freshData := &MarketData{
		Symbol:       result.Symbol,
		PriceUSD:     result.PriceUSD,
		PriceIDR:     priceIDR,
		ChangePct24h: result.ChangePct24h,
		LastFetched:  result.FetchedAt,
		Source:       result.Source,
	}

	if err := c.SetMarketData(ctx, freshData); err != nil {
		log.Printf("[ERROR] GetMarketData: persist fresh data for %s: %v", symbol, err)
	}

	return freshData, nil
}

func (c *Cache) getFromRedis(ctx context.Context, symbol string) (*MarketData, error) {
	raw, err := c.redis.Get(ctx, marketKey(symbol)).Bytes()
	if err != nil {
		return nil, err
	}

	var data MarketData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("unmarshal cached market data for %s: %w", symbol, err)
	}
	return &data, nil
}

func (c *Cache) getFromMySQL(ctx context.Context, symbol string) (data *MarketData, fresh bool, err error) {
	var d MarketData
	err = c.db.QueryRowContext(ctx, `
		SELECT symbol, price_usd, price_idr, change_pct_24h, last_fetched, source
		FROM market_cache
		WHERE symbol = ?`, symbol,
	).Scan(&d.Symbol, &d.PriceUSD, &d.PriceIDR, &d.ChangePct24h, &d.LastFetched, &d.Source)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("query market_cache for %s: %w", symbol, err)
	}

	isFresh := time.Since(d.LastFetched) < time.Duration(c.ttlHours)*time.Hour
	return &d, isFresh, nil
}

func (c *Cache) setRedis(ctx context.Context, data *MarketData) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal market data for %s: %w", data.Symbol, err)
	}

	ttl := time.Duration(c.ttlHours) * time.Hour
	if err := c.redis.Set(ctx, marketKey(data.Symbol), raw, ttl).Err(); err != nil {
		return fmt.Errorf("set redis for %s: %w", data.Symbol, err)
	}
	return nil
}

// SetMarketData implements the write flow used by the scheduler on every
// run: it updates Redis (TTL = ttlHours) and MySQL's market_cache table
// together.
func (c *Cache) SetMarketData(ctx context.Context, data *MarketData) error {
	if err := c.setRedis(ctx, data); err != nil {
		return fmt.Errorf("cache: %w", err)
	}

	_, err := c.db.ExecContext(ctx, `
		INSERT INTO market_cache (symbol, price_usd, price_idr, change_pct_24h, last_fetched, source)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			price_usd = VALUES(price_usd),
			price_idr = VALUES(price_idr),
			change_pct_24h = VALUES(change_pct_24h),
			last_fetched = VALUES(last_fetched),
			source = VALUES(source)`,
		data.Symbol, data.PriceUSD, data.PriceIDR, data.ChangePct24h, data.LastFetched, data.Source,
	)
	if err != nil {
		return fmt.Errorf("cache: upsert market_cache for %s: %w", data.Symbol, err)
	}

	return nil
}

// GetAllCachedSymbols lists every symbol currently cached in Redis under
// the market: prefix.
func (c *Cache) GetAllCachedSymbols(ctx context.Context) ([]string, error) {
	var symbols []string

	iter := c.redis.Scan(ctx, 0, marketKeyPrefix+"*", 0).Iterator()
	for iter.Next(ctx) {
		symbols = append(symbols, strings.TrimPrefix(iter.Val(), marketKeyPrefix))
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("cache: scan redis keys: %w", err)
	}

	return symbols, nil
}
