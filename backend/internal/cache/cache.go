// Package cache layers market data across Redis (primary, TTL-bound) and
// MySQL's market_cache table (persistent fallback), per WatchTower's
// read/write flow:
//
// Read: an entry younger than the caller's max age (default: the TTL) is
// served from Redis or MySQL. Older entries are refetched from the
// external API. A manual run ("Check now", max age ManualMaxAge) spends
// the instance's manual Twelve Data budget to refresh a stock, and when a
// manual refresh is refused or fails it falls back to an entry still
// within the TTL, marked Stale.
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
	"github.com/karvin-nanda/watchtower/internal/quota"
)

const (
	// v2: entries written before migration 000013 stored an unknown 24h
	// change as 0 (fake "0.00%" for gold). Bumping the prefix makes every
	// pre-upgrade entry a miss, so the corrected MySQL row is used (#30).
	marketKeyPrefix = "market:v2:"
	week52KeyPrefix = "week52:"
	week52TTL       = 24 * time.Hour

	// Gold has no 24h change at the source; it is computed from our own
	// snapshots, taken on every fresh fetch (spec §6.1).
	snapshotRetention = 8 * 24 * time.Hour
	changeWindowMin   = 20 * time.Hour
	changeWindowMax   = 28 * time.Hour
	// The 7-day change picks the snapshot closest to 7 d ago within 6-8 d.
	// One SELECT from changeWindowMin to weekWindowMax feeds both picks.
	weekWindowMin = 6 * 24 * time.Hour
	weekWindowMax = snapshotRetention

	closes7dKeyPrefix = "asset:closes7d:"
)

// ManualMaxAge is the oldest price a manual run accepts (spec Q2).
const ManualMaxAge = 15 * time.Minute

// FetchOptions controls how fresh GetMarketData's answer must be.
type FetchOptions struct {
	// MaxAge refetches entries older than this; 0 means the TTL (periodic runs).
	MaxAge time.Duration
	// Manual marks a user's Check now (see the package doc).
	Manual bool
}

// marketFetcher is the subset of asset.AssetFetcher the cache uses.
type marketFetcher interface {
	FetchAsset(ctx context.Context, symbol, assetType string) (*asset.FetchResult, error)
	FetchCryptoWeek52(ctx context.Context, symbol string) (high, low float64, err error)
	FetchStockCloses(ctx context.Context, symbol string) ([]asset.DailyClose, error)
}

// manualBudget is the subset of quota.Budget the cache uses.
type manualBudget interface {
	TryUse(ctx context.Context, credits int) (bool, error)
}

// MarketData is the cached market snapshot for a single asset symbol,
// mirroring the market_cache table. Nil pointers mean unknown.
type MarketData struct {
	Symbol        string     `json:"symbol"`
	PriceUSD      float64    `json:"price_usd"`
	PriceIDR      float64    `json:"price_idr"`
	ChangePct24h  *float64   `json:"change_pct_24h"`
	ChangePct7d   *float64   `json:"change_pct_7d"`
	Week52HighUSD *float64   `json:"week52_high_usd"`
	Week52LowUSD  *float64   `json:"week52_low_usd"`
	HighUSD       *float64   `json:"high_usd"`
	HighSince     *time.Time `json:"high_since"`
	// Stale marks data served because a manual refresh was refused or
	// failed. It is never persisted.
	Stale       bool      `json:"-"`
	LastFetched time.Time `json:"last_fetched"`
	Source      string    `json:"source"`
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
	fetcher  marketFetcher
	budget   manualBudget
	now      func() time.Time     // nil = time.Now
	rateFn   func() currency.Rate // nil = currency.Current
}

// New builds a Cache. ttlHours is both the Redis TTL and the default
// freshness window (ASSET_SCHEDULER_INTERVAL_HOURS). budget limits the
// Twelve Data credits manual runs may spend.
func New(redisClient *redis.Client, db *sql.DB, ttlHours int, fetcher *asset.AssetFetcher, budget *quota.Budget) *Cache {
	return &Cache{redis: redisClient, db: db, ttlHours: ttlHours, fetcher: fetcher, budget: budget}
}

func (c *Cache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Cache) rate() currency.Rate {
	if c.rateFn != nil {
		return c.rateFn()
	}
	return currency.Current()
}

func marketKey(symbol string) string {
	return marketKeyPrefix + symbol
}

// GetMarketData returns symbol's market data no older than opts allows.
// assetType ("stock", "crypto" or "gold") picks the external API for a
// refetch.
func (c *Cache) GetMarketData(ctx context.Context, symbol, assetType string, opts FetchOptions) (*MarketData, error) {
	ttl := time.Duration(c.ttlHours) * time.Hour
	maxAge := opts.MaxAge
	if maxAge <= 0 {
		maxAge = ttl
	}

	cached, fromRedis := c.cachedEntry(ctx, symbol)
	if cached != nil && c.clock().Sub(cached.LastFetched) < maxAge {
		if !fromRedis {
			if err := c.setRedis(ctx, cached); err != nil {
				log.Printf("[ERROR] GetMarketData: warm redis for %s: %v", symbol, err)
			}
		}
		return cached, nil
	}
	// What a periodic run would still accept: the fallback for a manual
	// refresh that cannot happen.
	usable := cached != nil && c.clock().Sub(cached.LastFetched) < ttl

	if opts.Manual && assetType == "stock" {
		ok, err := false, error(nil)
		if c.budget != nil {
			ok, err = c.budget.TryUse(ctx, 1)
		}
		if err != nil {
			log.Printf("[ERROR] GetMarketData: manual budget for %s: %v", symbol, err)
		}
		if !ok {
			if usable {
				return markStale(cached), nil
			}
			return nil, fmt.Errorf("cache: no manual Twelve Data budget left to refresh %s", symbol)
		}
	}

	fresh, err := c.refresh(ctx, symbol, assetType, opts.Manual)
	if err != nil {
		if opts.Manual && usable {
			log.Printf("[WARN] GetMarketData: manual refresh of %s failed, using cached data: %v", symbol, err)
			return markStale(cached), nil
		}
		return nil, err
	}
	return fresh, nil
}

// cachedEntry returns the Redis entry, else the MySQL row (fromRedis =
// false), else nil. Read errors are logged and treated as a miss.
func (c *Cache) cachedEntry(ctx context.Context, symbol string) (data *MarketData, fromRedis bool) {
	if data, err := c.getFromRedis(ctx, symbol); err == nil {
		return data, true
	} else if !errors.Is(err, redis.Nil) {
		log.Printf("[ERROR] GetMarketData: redis get %s: %v", symbol, err)
	}
	data, err := c.getFromMySQL(ctx, symbol)
	if err != nil {
		log.Printf("[ERROR] GetMarketData: mysql get %s: %v", symbol, err)
	}
	return data, false
}

func markStale(d *MarketData) *MarketData {
	cp := *d
	cp.Stale = true
	return &cp
}

// refresh fetches symbol from its external API and stores it in both layers.
// manual is true for a user's Check now: the stock 7-day reference then
// spends its own manual budget credit.
func (c *Cache) refresh(ctx context.Context, symbol, assetType string, manual bool) (*MarketData, error) {
	result, err := c.fetcher.FetchAsset(ctx, symbol, assetType)
	if err != nil {
		return nil, fmt.Errorf("cache: refresh %s from external api: %w", symbol, err)
	}

	rate := c.rate()
	if result.PriceIDR == 0 && rate.Fallback {
		log.Printf("[WARN] GetMarketData: no real USD/IDR rate for %s; IDR price stored as unknown (0)", symbol)
	}

	freshData := &MarketData{
		// The requested symbol, not result.Symbol, so the cache key always
		// matches what the caller asked for (FetchGold reports a fixed one).
		Symbol:        symbol,
		PriceUSD:      result.PriceUSD,
		PriceIDR:      displayIDR(result.PriceIDR, result.PriceUSD, rate),
		ChangePct24h:  result.ChangePct24h,
		ChangePct7d:   result.ChangePct7d,
		Week52HighUSD: result.Week52HighUSD,
		Week52LowUSD:  result.Week52LowUSD,
		HighUSD:       result.HighUSD,
		HighSince:     result.HighSince,
		LastFetched:   result.FetchedAt,
		Source:        result.Source,
	}

	if assetType == "gold" && result.PriceIDR > 0 {
		day, week, err := c.goldChangeFromSnapshots(ctx, symbol, result.PriceIDR, result.FetchedAt)
		if err != nil {
			log.Printf("[ERROR] GetMarketData: gold change for %s: %v", symbol, err)
		}
		freshData.ChangePct24h = day
		freshData.ChangePct7d = week
	}
	if assetType == "stock" {
		freshData.ChangePct7d = c.stockChange7d(ctx, symbol, result.PriceUSD, result.SessionDay, manual)
	}

	if err := c.SetMarketData(ctx, freshData); err != nil {
		log.Printf("[ERROR] GetMarketData: persist fresh data for %s: %v", symbol, err)
	}
	return freshData, nil
}

// goldChangeFromSnapshots stores priceIDR as a snapshot, prunes snapshots
// older than snapshotRetention, and returns the change against the
// snapshot closest to 24 h ago (day, nil when none is in the 20-28 h
// window) and against the one closest to 7 d ago (week, nil when none is
// in the 6-8 d window). Changes are computed in IDR so USD/IDR movement
// does not leak in.
func (c *Cache) goldChangeFromSnapshots(ctx context.Context, symbol string, priceIDR float64, now time.Time) (day, week *float64, err error) {
	if _, err := c.db.ExecContext(ctx,
		`INSERT INTO price_snapshots (symbol, price, currency, observed_at) VALUES (?, ?, 'IDR', ?)`,
		symbol, priceIDR, now,
	); err != nil {
		return nil, nil, fmt.Errorf("insert price snapshot: %w", err)
	}
	if _, err := c.db.ExecContext(ctx,
		`DELETE FROM price_snapshots WHERE symbol = ? AND observed_at < ?`,
		symbol, now.Add(-snapshotRetention),
	); err != nil {
		log.Printf("[ERROR] goldChangeFromSnapshots: prune %s: %v", symbol, err)
	}

	// One query covers both windows; the pick functions filter by age.
	rows, err := c.db.QueryContext(ctx,
		`SELECT price, observed_at FROM price_snapshots WHERE symbol = ? AND observed_at BETWEEN ? AND ?`,
		symbol, now.Add(-weekWindowMax), now.Add(-changeWindowMin),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("query price snapshots: %w", err)
	}
	defer rows.Close()

	var snaps []Snapshot
	for rows.Next() {
		var s Snapshot
		if err := rows.Scan(&s.Price, &s.ObservedAt); err != nil {
			return nil, nil, fmt.Errorf("scan price snapshot: %w", err)
		}
		snaps = append(snaps, s)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("read price snapshots: %w", err)
	}

	if prev, ok := pickDaySnapshot(snaps, now); ok && prev.Price != 0 {
		v := (priceIDR - prev.Price) / prev.Price * 100
		day = &v
	}
	if prev, ok := pickWeekSnapshot(snaps, now); ok && prev.Price != 0 {
		v := (priceIDR - prev.Price) / prev.Price * 100
		week = &v
	}
	return day, week, nil
}

// pickDaySnapshot returns the snapshot closest to 24 h before now among
// those observed between changeWindowMax and changeWindowMin ago.
func pickDaySnapshot(snaps []Snapshot, now time.Time) (Snapshot, bool) {
	return pickClosest(snaps, now, 24*time.Hour, changeWindowMin, changeWindowMax)
}

// pickWeekSnapshot returns the snapshot closest to 7 d before now among
// those observed between weekWindowMax and weekWindowMin ago.
func pickWeekSnapshot(snaps []Snapshot, now time.Time) (Snapshot, bool) {
	return pickClosest(snaps, now, 7*24*time.Hour, weekWindowMin, weekWindowMax)
}

// pickClosest returns the snapshot whose age is closest to target, among
// those with an age between minAge and maxAge.
func pickClosest(snaps []Snapshot, now time.Time, target, minAge, maxAge time.Duration) (Snapshot, bool) {
	want := now.Add(-target)
	var best Snapshot
	var bestDist time.Duration
	found := false
	for _, s := range snaps {
		age := now.Sub(s.ObservedAt)
		if age < minAge || age > maxAge {
			continue
		}
		dist := s.ObservedAt.Sub(want)
		if dist < 0 {
			dist = -dist
		}
		if !found || dist < bestDist {
			best, bestDist, found = s, dist, true
		}
	}
	return best, found
}

func closes7dKey(symbol string) string {
	return closes7dKeyPrefix + symbol
}

// untilNextUTCMidnight is how long a daily reference stays valid: until
// the next UTC midnight, but never less than a minute so a key written
// just before midnight is not born already expired.
func untilNextUTCMidnight(now time.Time) time.Duration {
	u := now.UTC()
	next := time.Date(u.Year(), u.Month(), u.Day()+1, 0, 0, 0, 0, time.UTC)
	d := next.Sub(u)
	if d < time.Minute {
		d = time.Minute
	}
	return d
}

// closes7dTimeout bounds the stock closes lookup. It has its own context
// so a slow /time_series call or Redis write does not run on the caller's
// per-symbol context.
const closes7dTimeout = 20 * time.Second

// stockChange7d returns the percent change of price against the stock's
// close about 7 days before sessionDay (the trading date of price), or nil
// when unknown. A zero sessionDay falls back to the newest cached bar.
//
// The last daily closes are cached in Redis until the next UTC midnight,
// and the reference is picked from that list on every call, so it follows
// the session of the price (before the US open the price is still the
// previous session's close). A miss costs one extra Twelve Data credit on
// top of the quote, so a manual run takes its own budget credit first. A
// failed fetch stores nothing, so the next refresh retries. A Redis read
// error returns nil without fetching, so Redis being down does not spend a
// credit on every refresh.
func (c *Cache) stockChange7d(ctx context.Context, symbol string, price float64, sessionDay time.Time, manual bool) *float64 {
	own, cancel := context.WithTimeout(context.Background(), closes7dTimeout)
	defer cancel()

	var closes []asset.DailyClose
	raw, err := c.redis.Get(own, closes7dKey(symbol)).Bytes()
	switch {
	case err == nil:
		if jsonErr := json.Unmarshal(raw, &closes); jsonErr != nil {
			log.Printf("[ERROR] stockChange7d: decode cached closes for %s: %v", symbol, jsonErr)
			closes = nil
		}
	case errors.Is(err, redis.Nil):
	default:
		log.Printf("[ERROR] stockChange7d: redis get %s: %v", symbol, err)
		return nil
	}

	if len(closes) == 0 {
		if manual {
			ok, err := false, error(nil)
			if c.budget != nil {
				ok, err = c.budget.TryUse(ctx, 1)
			}
			if err != nil {
				log.Printf("[ERROR] stockChange7d: manual budget for %s: %v", symbol, err)
			}
			if !ok {
				return nil
			}
		}
		fetched, err := c.fetcher.FetchStockCloses(own, symbol)
		if err != nil {
			log.Printf("[WARN] stockChange7d: daily closes for %s: %v", symbol, err)
			return nil
		}
		if len(fetched) == 0 {
			return nil
		}
		closes = fetched
		raw, err := json.Marshal(closes)
		if err == nil {
			err = c.redis.Set(own, closes7dKey(symbol), raw, untilNextUTCMidnight(c.clock())).Err()
		}
		if err != nil {
			log.Printf("[ERROR] stockChange7d: redis set %s: %v", symbol, err)
		}
	}

	if sessionDay.IsZero() {
		sessionDay = closes[0].Day
	}
	ref, ok := asset.PickRef7d(closes, sessionDay)
	if !ok || ref.Close <= 0 {
		return nil
	}
	change := (price - ref.Close) / ref.Close * 100
	return &change
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

	high, low, err = c.fetcher.FetchCryptoWeek52(ctx, symbol)
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

func (c *Cache) getFromMySQL(ctx context.Context, symbol string) (*MarketData, error) {
	var (
		d                              MarketData
		change, change7d, hi, lo, high sql.NullFloat64
		highSince                      sql.NullTime
	)
	err := c.db.QueryRowContext(ctx, `
		SELECT symbol, price_usd, price_idr, change_pct_24h, change_pct_7d, week52_high_usd, week52_low_usd, high_usd, high_since, last_fetched, source
		FROM market_cache
		WHERE symbol = ?`, symbol,
	).Scan(&d.Symbol, &d.PriceUSD, &d.PriceIDR, &change, &change7d, &hi, &lo, &high, &highSince, &d.LastFetched, &d.Source)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("query market_cache for %s: %w", symbol, err)
	}
	d.ChangePct24h = nullFloatPtr(change)
	d.ChangePct7d = nullFloatPtr(change7d)
	d.Week52HighUSD = nullFloatPtr(hi)
	d.Week52LowUSD = nullFloatPtr(lo)
	d.HighUSD = nullFloatPtr(high)
	if highSince.Valid {
		d.HighSince = &highSince.Time
	}
	return &d, nil
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
		INSERT INTO market_cache (symbol, price_usd, price_idr, change_pct_24h, change_pct_7d, week52_high_usd, week52_low_usd, high_usd, high_since, last_fetched, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			price_usd = VALUES(price_usd),
			price_idr = VALUES(price_idr),
			change_pct_24h = VALUES(change_pct_24h),
			change_pct_7d = VALUES(change_pct_7d),
			week52_high_usd = VALUES(week52_high_usd),
			week52_low_usd = VALUES(week52_low_usd),
			high_usd = VALUES(high_usd),
			high_since = VALUES(high_since),
			last_fetched = VALUES(last_fetched),
			source = VALUES(source)`,
		data.Symbol, data.PriceUSD, data.PriceIDR, data.ChangePct24h, data.ChangePct7d, data.Week52HighUSD, data.Week52LowUSD, data.HighUSD, data.HighSince, data.LastFetched, data.Source,
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

// displayIDR is the IDR price stored for the UI: the source's own IDR price
// when there is one (gold), otherwise the USD price at a real rate, and 0
// (unknown, shown as "—") when only the hardcoded fallback rate exists.
func displayIDR(nativeIDR, priceUSD float64, rate currency.Rate) float64 {
	if nativeIDR > 0 {
		return nativeIDR
	}
	if rate.Fallback || rate.Value <= 0 {
		return 0
	}
	return priceUSD * rate.Value
}
