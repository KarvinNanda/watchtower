# WatchTower Backend

Two Go processes share one MySQL database and one Redis:

- **API** (`cmd/api`, Gin): auth, the user's asset rules and keywords, notification history, the dashboard, the manual-run queue, and the Telegram `/start` replies that give users their chat ID (a private chat's or, for `/start@BotName` in a group, the group's).
- **Scheduler** (`cmd/scheduler`): on a persisted schedule it fetches market prices and security feeds, evaluates every active asset rule with a pure rule engine, matches sentinel items against keywords, and sends one batched Telegram message per user. It also runs the "Check now" and "Send report" jobs that the API queues.

There is no AI anywhere: alerts are built only from the data the sources provide and the user's own rules.

## Tech stack

| Component | Version | Purpose |
|---|---|---|
| Go | 1.25 (toolchain 1.25.11) | Language runtime |
| Gin | v1.12.0 | HTTP framework and routing |
| gin-contrib/cors | v1.7.7 | Credentialed CORS (required for the httpOnly auth cookie) |
| golang-jwt/jwt | v5.2.2 | JWT issuance and validation |
| golang.org/x/crypto | v0.52.0 | bcrypt password hashing |
| go-sql-driver/mysql | v1.8.1 | MySQL driver for `database/sql` |
| golang-migrate/migrate | v4.17.1 | Versioned SQL schema migrations, run on every API and scheduler start |
| redis/go-redis | v9.6.3 | Market data cache, manual-run queue, scheduler heartbeat, Twelve Data budget |
| spf13/viper | v1.19.0 | Configuration loading (env vars + YAML defaults) |
| joho/godotenv | v1.5.1 | `.env` file loading |
| golang.org/x/text | v0.38.0 | Locale-aware number formatting in Telegram messages |
| stretchr/testify | v1.11.1 | Test assertions |
| DATA-DOG/go-sqlmock | v1.5.2 | Database mocking in tests |
| alicebob/miniredis | v2.39.0 | In-memory Redis for tests |

## Project structure

```
backend/
├── cmd/
│   ├── api/            # HTTP API server: route registration, request handlers, Telegram polling/webhook
│   ├── scheduler/      # Scheduler: asset and sentinel pipelines, manual-run consumer, heartbeat
│   ├── test_fetch/     # Manual debugging tool for internal/asset fetching (calls the real APIs)
│   └── test_sentinel/  # Manual debugging tool for internal/sentinel fetching
├── internal/
│   ├── alertmsg/       # Builds the Telegram MarkdownV2 messages: asset alerts, status reports, sentinel items; splits at 4096 bytes
│   ├── asset/          # Market data fetchers (CoinGecko, Twelve Data, harga-emas.org), the 42-symbol whitelist, API pacing
│   ├── auth/           # Registration, login, JWT in an httpOnly cookie, bcrypt hashing, auth middleware
│   ├── cache/          # Redis-first / MySQL-fallback market data cache, freshness rules for manual runs, gold price snapshots
│   ├── config/         # Environment/YAML configuration with strict number and boolean parsing
│   ├── currency/       # USD → IDR rate from open.er-api.com, cached; never shows the hardcoded fallback rate
│   ├── db/             # MySQL/Redis connections and the migration runner
│   ├── middleware/     # Per-IP rate limiting, SQL-injection pattern guard, security headers, request size limit
│   ├── notifier/       # Telegram sending for both bots and the notification_logs record
│   ├── quota/          # Daily Twelve Data credit budget for manual stock refreshes (Redis, UTC day)
│   ├── rules/          # Pure rule engine: price level (USD or IDR), 24h change, trailing stop, 52-week break; distances
│   ├── runs/           # Manual-run queue: POST/GET /runs, per-user locks, consumer, scheduler heartbeat
│   ├── scheduler/      # Pipelines, asset worker, status report, sentinel worker, run executor
│   ├── sentinel/       # Seven threat-intel sources and keyword matching
│   ├── telegram/       # Telegram Bot API client, /start handler, long polling and webhook
│   ├── user/           # Profile, asset rules, keywords, notification history, dashboard and market snapshot queries
│   └── utils/          # The hardened HTTP client used by every outbound fetcher
├── migrations/         # golang-migrate SQL migrations, numbered up/down pairs (000001–000022)
├── configs/config.yaml # Default configuration values, overridden by environment variables
├── .env.example
├── Makefile
└── go.mod / go.sum
```

## Asset rules

Each rule belongs to one user and one whitelisted symbol (20 crypto, 20 US stocks plus the SPY S&P 500 ETF, ANTAM gold):

| Rule type | Fires when | Notes |
|---|---|---|
| `price_level` | The price crosses a level (above or below) | Level in USD or IDR. Optional label: buy target, take profit, stop loss. Fires once per crossing. |
| `pct_change_24h` | The 24h change passes ±pct | Cooldown and price-move check against repeats |
| `trailing_stop` | The price drops pct from the highest price since tracking started | Fires once, then needs a re-arm. Gold (ANTAM) is tracked on its rupiah price. |
| `week52_break` | The price passes the 52-week extreme stored at the previous check | Not available for gold |

IDR levels and gold trailing stops are evaluated only on a real rupiah price; without one they wait instead of using the hardcoded fallback rate. Alerts are information only, never advice.

## API endpoints

All routes live under `/api` with no version prefix. "Cookie" means the request must carry a valid `watchtower_token` httpOnly cookie, set by `/api/auth/login` and checked by `AuthMiddleware`. Every cookie route is also limited to 100 requests/min per IP.

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/health` | No | Liveness check: pings MySQL and Redis |
| POST | `/api/auth/register` | No | Create an account; 5 requests/min per IP |
| POST | `/api/auth/login` | No | Verify credentials and set the `watchtower_token` cookie; 10 requests/min per IP |
| GET | `/api/auth/me` | Cookie | The current user, used to restore the session after a page refresh |
| POST | `/api/auth/logout` | No | Clear the cookie |
| GET | `/api/user/profile` | Cookie | Profile: Telegram chat IDs, alert cooldown, language, devices/OS/expertise |
| PUT | `/api/user/profile` | Cookie | Update profile fields |
| GET | `/api/rules/assets` | Cookie | The user's asset rules with their state (peak, level currency, 52-week reference, fired) and the scheduler interval |
| POST | `/api/rules/assets` | Cookie | Create a rule; the symbol must be whitelisted and match its asset type |
| PUT | `/api/rules/assets/:id` | Cookie | Edit a rule and/or toggle `is_active`; a definition change resets its state |
| DELETE | `/api/rules/assets/:id` | Cookie | Delete a rule |
| POST | `/api/rules/assets/:id/rearm` | Cookie | Re-arm a fired trailing stop |
| GET | `/api/subscriptions/keywords` | Cookie | The user's sentinel keywords |
| POST | `/api/subscriptions/keywords` | Cookie | Add a keyword |
| DELETE | `/api/subscriptions/keywords/:id` | Cookie | Delete a keyword |
| GET | `/api/market/snapshot` | Cookie | Latest cached price for every symbol the user has rules for (active or paused) |
| GET | `/api/dashboard` | Cookie | Rule and keyword counts, last alert times, market snapshot, recent notifications |
| GET | `/api/notifications` | Cookie | Paginated delivery history, optional `type` (`asset`/`sentinel`) |
| POST | `/api/runs` | Cookie | Queue a manual run: `asset` (Check now), `asset_report` (status report) or `sentinel`. Needs a live scheduler and a linked chat; one per type per user every 5 minutes |
| GET | `/api/runs/:id` | Cookie | Status and summary of the user's own run |
| POST | `/webhook/telegram/asset` | No | Telegram webhook for the asset bot, only when `TELEGRAM_MODE=webhook`; 100 requests/min per IP shared with the sentinel webhook |
| POST | `/webhook/telegram/sentinel` | No | Same for the sentinel bot |

## Environment variables

Numbers and booleans are parsed strictly: an invalid value stops the API and scheduler at startup and names the variable.

| Variable | Required | Default | Description |
|---|---|---|---|
| `APP_ENV` | No | `development` | `development` or `production` (gin release mode; `JWT_SECRET` becomes required) |
| `APP_PORT` | No | `8080` | Port the API server listens on |
| `APP_TIMEZONE` | No | `Asia/Jakarta` | IANA timezone for timestamps in Telegram messages |
| `FRONTEND_URL` | No | *(empty)* | Extra CORS-allowed origin (e.g. `http://localhost:8080`), on top of the Vite dev origin `http://localhost:5173` |
| `COOKIE_SECURE` | No | `false` | Auth cookie `Secure` flag plus `SameSite=Strict`. Keep `false` on `http://localhost`; `true` only behind HTTPS |
| `TRUSTED_PROXIES` | No | *(empty)* | IPs/CIDRs allowed to set `X-Forwarded-For` for the per-IP rate limits |
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
| `TELEGRAM_ASSET_BOT_TOKEN` | Yes, for asset alerts | *(empty)* | Bot token for asset alerts and reports (see the root README for how to get it) |
| `TELEGRAM_SENTINEL_BOT_TOKEN` | Yes, for sentinel alerts | *(empty)* | Bot token for security alerts |
| `TELEGRAM_MODE` | No | `polling` | `polling` (one running instance per token) or `webhook` (registers the `/webhook/telegram/*` routes) |
| `TWELVE_DATA_API_KEY` | Yes, for stock prices | *(empty)* | US stock prices |
| `TWELVE_DATA_MANUAL_DAILY_CREDITS` | No | `400` | Twelve Data credits per UTC day that Check now may spend refreshing stale stock prices; `0` turns manual stock refresh off |
| `COINGECKO_BASE_URL` | No | `https://api.coingecko.com/api/v3` | Crypto pricing API base URL. The keyless public API allows roughly 10–30 calls per minute depending on load; WatchTower spaces CoinGecko calls 2.5 s apart, so many crypto symbols on a busy day can still get a 429 |
| `COINGECKO_API_KEY` | No | *(empty)* | Optional free CoinGecko Demo key, sent as the `x-cg-demo-api-key` header. Allows 100 calls/min and 10,000 calls/month; with a key CoinGecko calls are spaced 1.2 s apart. Many crypto symbols polled every few hours can use up the monthly calls, after which CoinGecko answers 429 |
| `OPEN_ER_BASE_URL` | No | `https://open.er-api.com/v6/latest/USD` | USD→IDR exchange rate API base URL |
| `GITHUB_TOKEN` | No | *(empty)* | Raises the sentinel's GitHub API rate limit from 10 to 30 req/min |
| `ASSET_SCHEDULER_INTERVAL_HOURS` | No | `4` | How often the asset pipeline runs |
| `SENTINEL_SCHEDULER_INTERVAL_HOURS` | No | `6` | How often the sentinel pipeline runs |
| `MAX_UNIQUE_SYMBOLS` | No | `100` | Cap on distinct asset symbols with an active rule, counted across **all users** because they share the instance's price-data quota. A new rule on an untracked symbol is refused at the cap; if the cap is lowered below what is already tracked, the scheduler checks the symbols whose rules are oldest and logs the skipped ones |
| `ALERT_PRICE_MOVE_PCT` | No | `3` | Price move (%) since the last alert that lets a repeat alert through an active cooldown |
| `ALERT_COOLDOWN_HOURS` | No | `4` | Alert cooldown a new account starts with; each user changes their own on the Profile page. Must be 0 or more |

## Database

All tables are InnoDB / `utf8mb4`. Migrations run automatically on start.

| Table | Description |
|---|---|
| `users` | Account: email, bcrypt hash, the two Telegram chat IDs, alert cooldown, preferred language |
| `user_profiles` | Devices, OS list and expertise level from the Profile page; not used by any alert |
| `asset_rules` | One row per rule: definition (type, direction, label, level in USD or IDR, pct) and the scheduler's state (baseline, last crossing, peak in USD or rupiah, 52-week reference, cooldown, fired price and time) |
| `keyword_subscriptions` | One row per sentinel keyword per user |
| `market_cache` | Latest fetched data per symbol (USD and IDR price, 24h change, 52-week range, recent high), the MySQL layer behind Redis |
| `price_snapshots` | Gold price history used to compute ANTAM's 24h change |
| `sentinel_seen_items` | Global dedup of sentinel items (7-day expiry), shared by all users |
| `notification_logs` | Delivery record for alerts and status reports, behind the history page and dashboard |
| `scheduler_state` | Last run of each pipeline, so a run missed during sleep happens once on wake |
| `asset_subscriptions`, `alert_states` | Legacy, from before the rule engine; no code reads them. Dropping them is tracked in #22 |

## Development

```bash
go mod download
make run-api         # API on :8080, runs migrations on start
make run-scheduler   # scheduler
make test            # go test ./... -v -count=1
make migrate-status  # current migration version
make sca             # govulncheck
```

## Security

- JWT in an httpOnly cookie, never readable by frontend JavaScript
- bcrypt cost 12, plus a dummy hash on unknown emails so login timing does not reveal which accounts exist
- Per-IP rate limits: register 5/min, login 10/min, every authenticated route 100/min, Telegram webhooks 100/min shared
- SQL-injection pattern guard on query and path parameters as defense in depth; every query is parameterized
- Symbol whitelist (42 symbols) checked before any market-data API is called
- Users only ever see market data for symbols they have rules for
- Secrets (bot tokens, API keys) are read from the environment only and sent in request headers, never in a URL, so a logged network error cannot contain them
- CI runs `govulncheck` and `gosec` on every pull request

## Testing

```bash
make test             # go test ./... -v -count=1
make test-coverage    # generates coverage.html
make test-race        # race detector
make test-new         # just the sentinel source parser tests (fast)
```

The migration test runs against a real MySQL when `MIGRATION_TEST_DSN` is set (`go test -tags integration ./internal/db/`). Every other test runs without MySQL, Redis or the network: database code uses `go-sqlmock`, Redis code uses `miniredis`, HTTP fetchers use `net/http/httptest`, and Telegram messages are pinned by golden files in `internal/alertmsg/testdata` (regenerate with `go test ./internal/alertmsg/ -update` and read the diff).

Coverage by package (`go test ./internal/... -cover`):

| Package | Coverage |
|---|---|
| `internal/rules` | 97.0% |
| `internal/alertmsg`, `internal/quota` | 94.4% |
| `internal/middleware` | 92.7% |
| `internal/config` | 90.9% |
| `internal/currency` | 88.9% |
| `internal/user` | 85.1% |
| `internal/auth` | 82.0% |
| `internal/cache` | 81.2% |
| `internal/runs` | 80.6% |
| `internal/scheduler` | 80.3% |
| `internal/asset` | 72.6% |
| `internal/sentinel` | 23.5% |
| `internal/telegram` | 0.8% |
