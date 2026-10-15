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

const (
	marketKeyPrefix = "market:"
	week52KeyPrefix = "week52:"
	week52TTL       = 24 * time.Hour

	// Gold has no 24h change at the source; it is computed from our own
	// snapshots, taken on every fresh fetch (spec §6.1).
	snapshotRetention = 8 * 24 * time.Hour
	changeWindowMin   = 20 * time.Hour
	changeWindowMax   = 28 * time.Hour
)

// MarketData is the cached market snapshot for a single asset symbol,
// mirroring the market_cache table. Nil pointers mean unknown.
type MarketData struct {
	Symbol        string    `json:"symbol"`
	PriceUSD      float64   `json:"price_usd"`
	PriceIDR      float64   `json:"price_idr"`
	ChangePct24h  *float64  `json:"change_pct_24h"`
	Week52HighUSD *float64  `json:"week52_high_usd"`
	Week52LowUSD  *float64  `json:"week52_low_usd"`
	LastFetched   time.Time `json:"last_fetched"`
	Source        string    `json:"source"`
}

// Snapshot is one row of price_snapshots, in the source currency.
type Snapshot struct {
	Price      float64
	ObservedAt time.Time
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

	priceIDR := result.PriceIDR
	if priceIDR == 0 {
		if priceIDR, err = currency.ConvertToIDR(result.PriceUSD); err != nil {
			log.Printf("[ERROR] GetMarketData: convert to IDR for %s: %v", symbol, err)
		}
	}

	freshData := &MarketData{
		// The requested symbol, not result.Symbol: FetchGold always
		// reports "XAU", which made "ANTAM" a permanent cache miss.
		Symbol:        symbol,
		PriceUSD:      result.PriceUSD,
		PriceIDR:      priceIDR,
		ChangePct24h:  result.ChangePct24h,
		Week52HighUSD: result.Week52HighUSD,
		Week52LowUSD:  result.Week52LowUSD,
		LastFetched:   result.FetchedAt,
		Source:        result.Source,
	}

	if assetType == "gold" && result.PriceIDR > 0 {
		change, err := c.goldChangeFromSnapshots(ctx, symbol, result.PriceIDR, result.FetchedAt)
		if err != nil {
			log.Printf("[ERROR] GetMarketData: gold change for %s: %v", symbol, err)
		}
		freshData.ChangePct24h = change
	}

	if err := c.SetMarketData(ctx, freshData); err != nil {
		log.Printf("[ERROR] GetMarketData: persist fresh data for %s: %v", symbol, err)
	}

	return freshData, nil
}

// goldChangeFromSnapshots stores priceIDR as a snapshot, prunes snapshots
// older than snapshotRetention, and returns the change against the
// snapshot closest to 24 h ago (nil when none is in the 20-28 h window).
// The change is computed in IDR so USD/IDR movement does not leak in.
func (c *Cache) goldChangeFromSnapshots(ctx context.Context, symbol string, priceIDR float64, now time.Time) (*float64, error) {
	if _, err := c.db.ExecContext(ctx,
		`INSERT INTO price_snapshots (symbol, price, currency, observed_at) VALUES (?, ?, 'IDR', ?)`,
		symbol, priceIDR, now,
	); err != nil {
		return nil, fmt.Errorf("insert price snapshot: %w", err)
	}
	if _, err := c.db.ExecContext(ctx,
		`DELETE FROM price_snapshots WHERE symbol = ? AND observed_at < ?`,
		symbol, now.Add(-snapshotRetention),
	); err != nil {
		log.Printf("[ERROR] goldChangeFromSnapshots: prune %s: %v", symbol, err)
	}

	rows, err := c.db.QueryContext(ctx,
		`SELECT price, observed_at FROM price_snapshots WHERE symbol = ? AND observed_at BETWEEN ? AND ?`,
		symbol, now.Add(-changeWindowMax), now.Add(-changeWindowMin),
	)
	if err != nil {
		return nil, fmt.Errorf("query price snapshots: %w", err)
	}
	defer rows.Close()

	var snaps []Snapshot
	for rows.Next() {
		var s Snapshot
		if err := rows.Scan(&s.Price, &s.ObservedAt); err != nil {
			return nil, fmt.Errorf("scan price snapshot: %w", err)
		}
		snaps = append(snaps, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read price snapshots: %w", err)
	}

	prev, ok := pickDaySnapshot(snaps, now)
	if !ok || prev.Price == 0 {
		return nil, nil
	}
	change := (priceIDR - prev.Price) / prev.Price * 100
	return &change, nil
}

// pickDaySnapshot returns the snapshot closest to 24 h before now among
// those observed between changeWindowMax and changeWindowMin ago.
func pickDaySnapshot(snaps []Snapshot, now time.Time) (Snapshot, bool) {
	target := now.Add(-24 * time.Hour)
	var best Snapshot
	var bestDist time.Duration
	found := false
	for _, s := range snaps {
		age := now.Sub(s.ObservedAt)
		if age < changeWindowMin || age > changeWindowMax {
			continue
		}
		dist := s.ObservedAt.Sub(target)
		if dist < 0 {
			dist = -dist
		}
		if !found || dist < bestDist {
			best, bestDist, found = s, dist, true
		}
	}
	return best, found
}

type week52Range struct {
	High float64 `json:"high"`
	Low  float64 `json:"low"`
}

// GetCryptoWeek52 returns a crypto symbol's 52-week daily-price range,
// cached in Redis for 24 h so it costs at most one CoinGecko call per coin
// per day.
func (c *Cache) GetCryptoWeek52(ctx context.Context, symbol string) (high, low float64, err error) {
	key := week52KeyPrefix + symbol
	raw, err := c.redis.Get(ctx, key).Bytes()
	if err == nil {
		var r week52Range
		if jsonErr := json.Unmarshal(raw, &r); jsonErr == nil {
			return r.High, r.Low, nil
		}
	} else if !errors.Is(err, redis.Nil) {
		log.Printf("[ERROR] GetCryptoWeek52: redis get %s: %v", symbol, err)
	}

	high, low, err = c.fetcher.FetchCryptoWeek52(symbol)
	if err != nil {
		return 0, 0, fmt.Errorf("cache: fetch 52-week range for %s: %w", symbol, err)
	}
	if b, mErr := json.Marshal(week52Range{High: high, Low: low}); mErr == nil {
		if sErr := c.redis.Set(ctx, key, b, week52TTL).Err(); sErr != nil {
			log.Printf("[ERROR] GetCryptoWeek52: redis set %s: %v", symbol, sErr)
		}
	}
	return high, low, nil
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
	var (
		d              MarketData
		change, hi, lo sql.NullFloat64
	)
	err = c.db.QueryRowContext(ctx, `
		SELECT symbol, price_usd, price_idr, change_pct_24h, week52_high_usd, week52_low_usd, last_fetched, source
		FROM market_cache
		WHERE symbol = ?`, symbol,
	).Scan(&d.Symbol, &d.PriceUSD, &d.PriceIDR, &change, &hi, &lo, &d.LastFetched, &d.Source)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("query market_cache for %s: %w", symbol, err)
	}
	d.ChangePct24h = nullFloatPtr(change)
	d.Week52HighUSD = nullFloatPtr(hi)
	d.Week52LowUSD = nullFloatPtr(lo)

	isFresh := time.Since(d.LastFetched) < time.Duration(c.ttlHours)*time.Hour
	return &d, isFresh, nil
}

func nullFloatPtr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	return &n.Float64
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
		INSERT INTO market_cache (symbol, price_usd, price_idr, change_pct_24h, week52_high_usd, week52_low_usd, last_fetched, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			price_usd = VALUES(price_usd),
			price_idr = VALUES(price_idr),
			change_pct_24h = VALUES(change_pct_24h),
			week52_high_usd = VALUES(week52_high_usd),
			week52_low_usd = VALUES(week52_low_usd),
			last_fetched = VALUES(last_fetched),
			source = VALUES(source)`,
		data.Symbol, data.PriceUSD, data.PriceIDR, data.ChangePct24h, data.Week52HighUSD, data.Week52LowUSD, data.LastFetched, data.Source,
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
