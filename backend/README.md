# WatchTower Backend

A Go + Gin REST API paired with a background scheduler engine: the API handles auth, subscriptions, and dashboard data, while the scheduler independently polls market prices and security threat-intel sources, evaluates every active subscription, and dispatches batched Telegram alerts.

## Tech Stack

| Component | Version | Purpose |
|---|---|---|
| Go | 1.25 (toolchain 1.25.11) | Language runtime |
| Gin | v1.12.0 | HTTP framework and routing |
| gin-contrib/cors | v1.7.7 | Credentialed CORS (required for the httpOnly auth cookie) |
| golang-jwt/jwt | v5.2.2 | JWT issuance and validation |
| golang.org/x/crypto | v0.52.0 | bcrypt password hashing |
| go-sql-driver/mysql | v1.8.1 | MySQL driver for `database/sql` |
| golang-migrate/migrate | v4.17.1 | Versioned SQL schema migrations |
| redis/go-redis | v9.6.3 | Redis client for the market data cache |
| spf13/viper | v1.19.0 | Configuration loading (env vars + YAML defaults) |
| joho/godotenv | v1.5.1 | `.env` file loading |
| golang.org/x/text | v0.38.0 | Locale-aware thousands-separator number formatting in Telegram messages |
| stretchr/testify | v1.11.1 | Test assertions |
| DATA-DOG/go-sqlmock | v1.5.2 | Database mocking in tests |

## Project Structure

```
backend/
├── cmd/
│   ├── api/            # HTTP API server entrypoint — route registration, request handlers
│   ├── scheduler/      # Background scheduler entrypoint — starts the asset and sentinel workers
│   ├── test_fetch/     # Manual debugging tool for internal/asset fetching
│   └── test_sentinel/  # Manual debugging tool for internal/sentinel fetching
├── internal/
│   ├── auth/           # Registration, login, JWT issuance via httpOnly cookie, bcrypt hashing, auth middleware
│   ├── user/           # User profile, asset/keyword subscriptions, notification history, dashboard queries
│   ├── asset/           # Market data fetcher (CoinGecko, Twelve Data, harga-emas.org scraping), RSI/volatility/trend calculation, DeepSeek market analyzer
│   ├── sentinel/        # Six-source threat-intel fetcher (NVD, CISA KEV, security RSS, GitHub repo search, Exploit-DB, GitHub Security Advisories), keyword matching, structured (JSON) AI output with a labeled-text fallback parser
│   ├── scheduler/       # asset_worker (4h interval, cooldown + price-move logic, one batched Telegram message per user) and sentinel_worker (6h interval, global item dedup)
│   ├── currency/        # USD → IDR exchange rate lookup, cached
│   ├── notifier/        # Telegram message dispatch for both bots, MarkdownV2 escaping, delivery logging
│   ├── telegram/        # Telegram Bot API client — shared handler logic, long-polling, webhook
│   ├── middleware/      # Per-IP rate limiting, SQL-injection pattern guard, security headers, request size limit
│   ├── cache/           # Redis-primary / MySQL-fallback market data cache
│   ├── config/          # Environment/YAML configuration loading
│   ├── db/              # MySQL/Redis connection setup and migration runner
│   └── utils/           # Shared helpers — the hardened HTTP client used by every outbound fetcher/analyzer
├── migrations/          # golang-migrate SQL migrations, numbered up/down pairs
├── configs/config.yaml  # Default configuration values, overridden by environment variables
├── .env.example
├── Makefile
└── go.mod / go.sum
```

## API Endpoints

All routes live directly under `/api` — there is no version prefix. "Cookie" in the Auth column means the request must carry a valid `watchtower_token` httpOnly cookie, set by `/api/auth/login` and validated by `AuthMiddleware`.

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/health` | No | Liveness check — pings MySQL and Redis, reports their status |
| POST | `/api/auth/register` | No | Create an account (email + password); rate-limited to 5 req/min per IP |
| POST | `/api/auth/login` | No | Verify credentials and set the `watchtower_token` cookie; rate-limited to 10 req/min per IP |
| GET | `/api/auth/me` | Cookie | Return the current user's identity — used by the frontend to restore session state after a page refresh |
| POST | `/api/auth/logout` | No | Clear the `watchtower_token` cookie |
| GET | `/api/user/profile` | Cookie | Fetch the full profile: devices, OS list, expertise level, Telegram chat IDs, cooldown, language |
| PUT | `/api/user/profile` | Cookie | Update profile fields |
| GET | `/api/subscriptions/assets` | Cookie | List the user's asset subscriptions |
| POST | `/api/subscriptions/assets` | Cookie | Create an asset subscription (symbol, alert type, thresholds) |
| PUT | `/api/subscriptions/assets/:id` | Cookie | Update an asset subscription |
| DELETE | `/api/subscriptions/assets/:id` | Cookie | Delete an asset subscription |
| GET | `/api/subscriptions/keywords` | Cookie | List the user's keyword subscriptions |
| POST | `/api/subscriptions/keywords` | Cookie | Create a keyword subscription |
| DELETE | `/api/subscriptions/keywords/:id` | Cookie | Delete a keyword subscription |
| GET | `/api/notifications` | Cookie | Paginated notification history, optional `type` (`asset`/`sentinel`) filter |
| GET | `/api/market/snapshot` | Cookie | Latest cached price for every symbol the user actively tracks |
| GET | `/api/market/:symbol` | Cookie | Latest cached price for a single symbol |
| GET | `/api/dashboard` | Cookie | Aggregated dashboard payload — subscription counts, last alert timestamps, market snapshot, recent notifications |
| POST | `/webhook/telegram/asset` | No | Telegram webhook callback for the asset bot — only registered when `TELEGRAM_MODE=webhook`; rate-limited (shared with the sentinel webhook) to 100 req/min per IP |
| POST | `/webhook/telegram/sentinel` | No | Telegram webhook callback for the sentinel bot — same conditions as above |

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `APP_ENV` | No | `development` | `development` or `production` — also drives the auth cookie's `Secure`/`SameSite` attributes |
| `APP_PORT` | No | `8080` | Port the API server listens on |
| `FRONTEND_URL` | Production only | *(empty)* | Additional CORS-allowed origin for the deployed frontend, on top of the fixed local-dev origins |
| `DB_HOST` | Yes | `127.0.0.1` | MySQL host |
| `DB_PORT` | No | `3306` | MySQL port |
| `DB_USER` | Yes | `root` | MySQL user |
| `DB_PASSWORD` | Yes | *(empty)* | MySQL password |
| `DB_NAME` | No | `watchtower` | MySQL database name |
| `REDIS_HOST` | Yes | `127.0.0.1` | Redis host |
| `REDIS_PORT` | No | `6379` | Redis port |
| `REDIS_PASSWORD` | No | *(empty)* | Redis password |
| `JWT_SECRET` | Yes outside development | *(empty)* | HMAC signing secret for issued JWTs |
| `JWT_EXPIRY_HOURS` | No | `24` | Token lifetime in hours; also the auth cookie's max age |
| `DEEPSEEK_API_KEY` | Yes, for AI commentary | *(empty)* | DeepSeek chat completions API key — without it, alerts still send but skip AI analysis |
| `DEEPSEEK_MODEL` | No | `deepseek-chat` | DeepSeek model name |
| `TELEGRAM_ASSET_BOT_TOKEN` | Yes, for asset alerts | *(empty)* | Bot token for asset price alerts |
| `TELEGRAM_SENTINEL_BOT_TOKEN` | Yes, for sentinel alerts | *(empty)* | Bot token for security alerts |
| `TELEGRAM_MODE` | No | `polling` | `polling` (local dev) or `webhook` (production; registers the `/webhook/telegram/*` routes) |
| `TWELVE_DATA_API_KEY` | Yes, for stock prices | *(empty)* | Stock/forex price data provider |
| `COINGECKO_BASE_URL` | No | `https://api.coingecko.com/api/v3` | Crypto pricing API base URL |
| `OPEN_ER_BASE_URL` | No | `https://open.er-api.com/v6/latest/USD` | USD→IDR exchange rate API base URL |
| `GITHUB_TOKEN` | No | *(empty)* | Raises the sentinel's GitHub API rate limit from 10 to 30 req/min |
| `ASSET_SCHEDULER_INTERVAL_HOURS` | No | `4` | How often the asset worker polls prices |
| `SENTINEL_SCHEDULER_INTERVAL_HOURS` | No | `6` | How often the sentinel worker polls threat-intel sources |
| `MAX_UNIQUE_SYMBOLS` | No | `100` | System-wide cap on distinct tracked asset symbols |
| `ALERT_COOLDOWN_HOURS` | No | `4` | Default minimum gap between repeat alerts for the same subscription |
| `ALERT_PRICE_MOVE_PCT` | No | `3` | Price move (%) since the last alert required to bypass an active cooldown |

## Database

Eight tables, all InnoDB / `utf8mb4`:

| Table | Description | Relations |
|---|---|---|
| `users` | Core account row: email, bcrypt password hash, per-bot Telegram chat IDs, alert cooldown, preferred language | Referenced by every table below |
| `user_profiles` | Devices, OS list (JSON arrays), and expertise level, used to ground sentinel AI analysis | 1:1 with `users` |
| `asset_subscriptions` | One row per (user, symbol) the user tracks, with alert type and price/percentage thresholds | N:1 with `users` |
| `keyword_subscriptions` | One row per keyword a user wants matched against incoming sentinel items | N:1 with `users` |
| `market_cache` | Latest fetched price per symbol — the MySQL fallback layer behind the Redis cache | Keyed by `symbol`, no foreign keys |
| `alert_states` | Last alert type/price/timestamp and cooldown expiry per (user, symbol), unique per pair | N:1 with `users` |
| `sentinel_seen_items` | Global dedup record per (source_type, item_identifier) with a 7-day expiry, so the same security item isn't reprocessed | No foreign keys — shared across all users |
| `notification_logs` | Append-only delivery record (asset or sentinel), backing the notification history API and dashboard | N:1 with `users` |

## Development

```bash
# Install dependencies
go mod download

# Run migrations
make migrate-up

# Start API server
make run-api

# Start scheduler
make run-scheduler

# Run tests
make test

# Run tests with coverage
make test-coverage

# Security scan
govulncheck ./...
```

## Security

- httpOnly cookie for the JWT — never exposed to frontend JavaScript, closing off token theft via XSS
- bcrypt cost 12 for password hashing
- Rate limiting: 5 req/min on register, 10 req/min on login, 100 req/min on general API routes, 100 req/min shared across the Telegram webhook routes
- SQL injection guard at the middleware layer, checking query/path parameters against known injection patterns (defense-in-depth — every actual query in this codebase is already parameterized)
- Input validation with a symbol whitelist (42 hardcoded symbols) before any external market-data API is called
- `govulncheck ./...` — clean, 0 vulnerabilities in called code

## Testing

```bash
make test             # go test ./... -v -count=1
make test-coverage     # generates coverage.html
make test-race         # race detector
make test-new          # just the RSI/batch-dedup/sentinel-parser tests added post-Phase-10
```

Tests never touch a real MySQL/Redis instance or the network — database-backed tests use `go-sqlmock`, HTTP-backed tests (DeepSeek, Twelve Data, currency conversion) use `net/http/httptest` or an injected mock client. Coverage is concentrated in the security-critical and pure-logic packages:

| Package | Coverage |
|---|---|
| `internal/middleware` | 90.3% |
| `internal/auth` | 78.1% |
| `internal/user` | 74.1% |
| `internal/asset` | 29.6% |
| `internal/sentinel` | 25.0% |
| `internal/scheduler` | 19.1% |
