# WatchTower — Backend

WatchTower is a monitoring and alerting service with two independent pipelines:

- **Asset Sentinel** — tracks stock/crypto/gold prices against user-defined thresholds (price bounds, % change) and notifies subscribers via Telegram, with optional AI-generated (DeepSeek) bilingual market commentary.
- **Security Sentinel** — polls threat-intel sources (NVD/NIST, CISA KEV, security RSS feeds, GitHub repos/advisories, Exploit-DB), extracts keywords, and notifies users subscribed to matching keywords.

The backend is a Go module exposing a REST API (Gin) plus two background scheduler binaries, backed by MySQL and Redis.

## Tech stack

| Concern            | Choice                                                   |
|--------------------|-----------------------------------------------------------|
| Language           | Go 1.23                                                    |
| HTTP framework     | [Gin](https://github.com/gin-gonic/gin)                    |
| Database           | MySQL (via `database/sql` + `go-sql-driver/mysql`)         |
| Migrations         | [golang-migrate](https://github.com/golang-migrate/migrate)|
| Cache              | Redis (`go-redis/v9`)                                      |
| Auth               | JWT (`golang-jwt/jwt/v5`) + bcrypt                          |
| Config             | Viper + `.env` (via `godotenv`)                             |
| AI commentary      | DeepSeek chat completions API                               |
| Notifications      | Telegram Bot API (long-polling or webhook)                  |
| Security scanning  | `govulncheck` (known-vuln dependencies) + `gosec` (static analysis) |

## Prerequisites

- Go 1.23 or newer
- MySQL 8.x
- Redis 6.x or newer
- (Optional) Docker — only needed for `make security`'s Nancy dependency-vulnerability sweep

## Getting started

1. **Clone and enter the backend directory** — all commands below (`go run`, `go test`, `make ...`) must be run from `backend/`, not from a subdirectory like `cmd/api/`.

2. **Copy the environment template and fill in your own values:**

   ```bash
   cp .env.example .env
   ```

   Open `.env` and set your own database credentials, JWT secret, DeepSeek API key, and Telegram bot tokens. See [Environment variables](#environment-variables) below for what each one does. Never commit `.env` — it's already excluded via `.gitignore`.

3. **Create the database** referenced by `DB_NAME` in your `.env`, then run migrations:

   ```bash
   make migrate-up
   ```

4. **Install dependencies:**

   ```bash
   go mod download
   ```

5. **Run the API server:**

   ```bash
   make run-api
   ```

   The server listens on `APP_PORT` (default `8080`) and exposes `GET /health` for a quick liveness check.

6. **Run the background scheduler** (asset price polling + sentinel threat-intel polling) in a separate terminal:

   ```bash
   make run-scheduler
   ```

## Project structure

```
backend/
├── cmd/
│   ├── api/            # HTTP API server entrypoint (routes, middleware wiring)
│   ├── scheduler/      # Background scheduler entrypoint (asset + sentinel workers)
│   ├── test_fetch/      # Manual debugging tool for internal/asset fetching
│   └── test_sentinel/   # Manual debugging tool for internal/sentinel fetching
├── internal/
│   ├── auth/           # Registration, login, JWT issuance/validation, auth middleware
│   ├── user/           # User profile, asset/keyword subscriptions, notification history
│   ├── asset/           # Market data fetching (stocks/crypto/gold) + DeepSeek analysis
│   ├── sentinel/        # Threat-intel fetching (NVD, CISA KEV, RSS, GitHub, Exploit-DB)
│   ├── currency/        # USD → IDR exchange rate conversion (cached)
│   ├── notifier/        # Outbound notification dispatch (Telegram formatting)
│   ├── telegram/        # Telegram Bot API client: handlers, long-polling, webhook
│   ├── scheduler/        # Worker loops that tie asset/sentinel fetching + alerting together
│   ├── middleware/       # HTTP hardening: security headers, rate limiting, CORS, SQLi guard
│   ├── cache/            # Redis-backed market data cache
│   ├── config/           # Environment/YAML configuration loading
│   └── db/               # MySQL/Redis connection setup + migration runner
├── migrations/          # golang-migrate SQL migrations (numbered, up/down pairs)
├── configs/
│   └── config.yaml      # Default configuration values (overridden by env vars)
├── .env.example         # Template for required environment variables (no real secrets)
├── Makefile
├── go.mod / go.sum
└── README.md
```

## Environment variables

All variables are documented with empty placeholders in `.env.example`. Copy it to `.env` and fill in your own values — do not commit real credentials.

| Variable | Purpose |
|---|---|
| `APP_ENV` | `development` or `production`. Controls CORS behavior and whether `JWT_SECRET` is mandatory. |
| `APP_PORT` | Port the API server listens on. |
| `FRONTEND_URL` | Allowed CORS origin when `APP_ENV=production`. Ignored in development (all origins allowed). |
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME` | MySQL connection. |
| `REDIS_HOST`, `REDIS_PORT`, `REDIS_PASSWORD` | Redis connection, used for market data caching. |
| `JWT_SECRET` | HMAC signing secret for issued JWTs. Required outside `development`. |
| `JWT_EXPIRY_HOURS` | Token lifetime in hours. |
| `DEEPSEEK_API_KEY`, `DEEPSEEK_MODEL` | DeepSeek chat completions API credentials, used for AI market commentary. |
| `TELEGRAM_ASSET_BOT_TOKEN`, `TELEGRAM_SENTINEL_BOT_TOKEN` | Bot tokens for the two separate Telegram bots (asset alerts vs. security alerts). |
| `TELEGRAM_MODE` | `polling` (default, for local development) or `webhook` (production; registers `POST /webhook/telegram/{asset,sentinel}`). |
| `TWELVE_DATA_API_KEY` | Stock/forex price data provider. |
| `COINGECKO_BASE_URL`, `OPEN_ER_BASE_URL` | Base URLs for crypto pricing and USD→IDR exchange rate, overridable for testing. |
| `GITHUB_TOKEN` | Optional; raises the sentinel's GitHub API rate limit from 10 to 30 req/min. |
| `ASSET_SCHEDULER_INTERVAL_HOURS`, `SENTINEL_SCHEDULER_INTERVAL_HOURS` | How often each background worker polls its sources. |
| `MAX_UNIQUE_SYMBOLS` | System-wide cap on distinct tracked asset symbols. |
| `ALERT_COOLDOWN_HOURS` | Minimum gap between repeat alerts for the same subscription. |
| `ALERT_PRICE_MOVE_PCT` | Default % change threshold used where applicable. |

## API overview

All routes are served under `/api` (no version prefix). Auth and general API routes are individually rate-limited (see [Security](#security)).

| Method | Path | Auth required |
|---|---|---|
| GET | `/health` | No |
| POST | `/api/auth/register` | No |
| POST | `/api/auth/login` | No |
| GET | `/api/auth/me` | Yes |
| GET / PUT | `/api/user/profile` | Yes |
| GET / POST | `/api/subscriptions/assets` | Yes |
| PUT / DELETE | `/api/subscriptions/assets/:id` | Yes |
| GET / POST | `/api/subscriptions/keywords` | Yes |
| DELETE | `/api/subscriptions/keywords/:id` | Yes |
| GET | `/api/notifications` | Yes |
| GET | `/api/market/snapshot` | Yes |
| GET | `/api/market/:symbol` | Yes |
| GET | `/api/dashboard` | Yes |

Authenticated routes require `Authorization: Bearer <token>`, where `<token>` is the JWT returned by `/api/auth/login`.

## Security

`internal/middleware` applies defense-in-depth HTTP hardening to every request:

- Standard security headers (`X-Content-Type-Options`, `X-Frame-Options`, HSTS, CSP, `Referrer-Policy`, `Permissions-Policy`)
- Per-IP rate limiting (fixed window, `sync.Map`-backed): 10 req/60s on auth endpoints, 100 req/60s on general API endpoints
- CORS: permissive in development, restricted to `FRONTEND_URL` in production
- Request body size limit (1 MB default)
- A basic SQL-injection pattern guard on query/path parameters (defense-in-depth only — every database query in this codebase uses parameterized/prepared statements)

`internal/auth` additionally enforces: strict email format validation, password complexity (min 8 chars, upper/lower/digit/special), timing-safe login (identical error and comparable latency for "wrong password" vs. "account doesn't exist", preventing user enumeration), and JWT hardening (rejects `alg: none`, requires `exp` and `iss` claims).

Run static security analysis locally:

```bash
make install-tools   # one-time: installs govulncheck + gosec
make sca             # known-vulnerability + static analysis scan
make security        # sca + a Sonatype Nancy dependency sweep (requires Docker)
```

A GitHub Actions workflow (`.github/workflows/sca.yml`) runs `govulncheck` and `gosec` automatically on pushes to `main`/`develop` and on PRs to `main`, uploading `gosec` findings to GitHub code scanning.

## Testing

```bash
make test            # go test ./... -v -count=1
make test-coverage    # generates coverage.html
make test-race        # race detector (requires a C toolchain / cgo)
```

Tests never touch a real MySQL/Redis instance or the network: database-backed tests use [`go-sqlmock`](https://github.com/DATA-DOG/go-sqlmock), and HTTP-backed tests (DeepSeek, currency conversion) use `net/http/httptest` or an injected mock client. `internal/auth`, `internal/middleware`, and `internal/user` are the packages with the heaviest test coverage, since they carry the security-critical logic.

## Database migrations

Migrations live in `migrations/` as numbered up/down SQL pairs, run via `golang-migrate`:

```bash
make migrate-up       # apply all pending migrations
make migrate-down     # roll back the most recent migration
make migrate-status    # print the current migration version
```

## Building binaries

```bash
make build
```

Produces `bin/api` and `bin/scheduler`.
