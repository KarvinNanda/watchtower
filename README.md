# WatchTower

AI-powered finance & security intelligence platform. Monitors crypto, stocks, and gold prices alongside CVE and security news — then delivers personalized summaries to your Telegram.

## The Problem

Price alerts and security news live in a dozen different places — exchange apps, NVD, CISA's KEV feed, security blogs, GitHub advisories — and none of them know what you personally care about. You end up either checking everything manually or missing the one alert that actually mattered, because generic feeds don't filter for your thresholds, your devices, or your risk tolerance.

## The Solution

WatchTower is a multi-user SaaS: each subscriber picks the assets and security keywords they want watched, sets their own price/percentage thresholds, and fills in a lightweight profile (devices, OS, expertise level). A background scheduler polls market data and threat-intel sources on independent intervals, evaluates every active subscription against fresh data, and — only when something actually crosses a threshold or matches a keyword — asks DeepSeek to turn it into a short, bilingual, context-aware summary. Every triggered alert for a user in a given run is batched into a single Telegram message instead of one message per symbol or item, so an active watchlist doesn't turn into a notification flood.

## Architecture

```
┌─────────────┐     ┌─────────────┐     ┌──────────┐
│  Vue.js 3   │────▶│   Gin API   │────▶│  MySQL   │
│  Dashboard  │     │  (Go 1.25)  │     │  Redis   │
└─────────────┘     └──────┬──────┘     └──────────┘
                           │
                    ┌──────▼──────┐
                    │  Scheduler  │
                    │ Asset (4h)  │
                    │Sentinel (6h)│
                    └──────┬──────┘
                           │
              ┌────────────┼────────────┐
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │ External │ │DeepSeek  │ │ Telegram │
        │   APIs   │ │    AI    │ │   Bots   │
        └──────────┘ └──────────┘ └──────────┘
```

## Tech Stack

| Layer | Technology | Purpose |
|---|---|---|
| Frontend | Vue.js 3, Naive UI, Pinia | Bilingual (EN/ID) SPA dashboard, dark mode by default |
| API | Go 1.25, Gin | REST API, JWT auth issued via httpOnly cookies |
| Scheduler | Go | Two independent background workers (asset, sentinel) |
| Database | MySQL 8 | Durable storage: users, subscriptions, alert state, history |
| Cache | Redis 7 | Hot read-through cache in front of MySQL for market prices |
| AI | DeepSeek chat completions API | Bilingual, per-context market and security analysis |
| Delivery | Telegram Bot API | Two separate bots (asset alerts, sentinel alerts) |
| Deployment | Docker Compose, Nginx | Containerized stack; Nginx reverse-proxies and terminates TLS in production |

## Key Technical Decisions

- **httpOnly cookie instead of localStorage for the JWT.** A token held in `localStorage` is readable by any script running on the page — one XSS bug anywhere in the dependency tree and every session is stealable. An httpOnly cookie is invisible to JavaScript entirely; the browser attaches it automatically and the backend reads it server-side.
- **One shared DeepSeek call per symbol/item, not per subscriber.** Both the asset and sentinel pipelines call DeepSeek once per triggered symbol or matched security item, aggregating the relevant subscribers' thresholds (asset) or devices/OS/expertise (sentinel) into that single prompt, rather than one call per (item, user) pair. Cost scales with distinct items, not with subscriber count, at the cost of every affected subscriber seeing analysis grounded in the aggregate audience rather than a fully individual prompt.
- **Redis + MySQL dual layer for market cache.** Redis serves hot reads fast; MySQL's `market_cache` table is the durable fallback so a Redis restart or cold cache doesn't mean a blank dashboard, and so the price data stays queryable directly with SQL for debugging.
- **golang-migrate instead of raw SQL scripts.** Every schema change is a numbered, reversible up/down pair, applied automatically on every `api`/`scheduler` container start. That removes the class of bug where dev, staging, and prod databases have quietly diverged because someone ran a one-off script by hand.
- **Two separate Telegram bots, not one.** Asset alerts and sentinel alerts have different tone, formatting, and (often) target audiences — a user can point sentinel alerts at a security-focused group chat and asset alerts at a personal DM. It also means a compromised bot token only exposes one alert stream, not both.

## Known Tradeoffs

- The JWT used to live in `localStorage`, which is exploitable via XSS — this has since been migrated to an httpOnly cookie (see Key Technical Decisions above), but it's worth naming as a gap that existed and was closed rather than pretending it never happened.
- The gold price source (`harga-emas.org`) is scraped from JSON-LD structured data on a public page with no official API. If the site changes its markup, gold quotes silently stop updating until the scraper is patched — accepted because there's no free, official Antam gold price API to fall back to.
- Twelve Data's free tier caps at 800 calls/day, so the asset symbol whitelist is hard-capped at 42 entries and `MAX_UNIQUE_SYMBOLS` defaults conservatively. RSI/volatility enrichment costs one extra Twelve Data call per stock per scheduler run, so this is the first thing that needs a paid plan if the tracked symbol list grows meaningfully.

## Services

| Service | Technology | Port |
|---|---|---|
| Frontend | Vue.js 3 + Nginx | 80 |
| API | Go + Gin | 8080 |
| Scheduler | Go | — |
| Database | MySQL 8 | 3306 |
| Cache | Redis 7 | 6379 |

## Quick Start

### Prerequisites

- Go 1.25+
- Node.js 20+
- MySQL 8.x and Redis 6+ (or let Docker Compose run them for you)
- Docker + Docker Compose, for the production stack

### Clone & Setup

```bash
git clone <repo-url> watchtower
cd watchtower
cp .env.example .env                     # root: mysql/redis credentials for docker-compose
cp backend/.env.example backend/.env     # backend: full app config (DB, JWT, DeepSeek, Telegram, ...)
cp frontend/.env.example frontend/.env   # frontend: API base URL, Telegram bot usernames
```

### Development

```bash
make dev-up                                    # starts mysql + redis via Docker
cd backend && go mod download && make migrate-up
make run-api                                   # terminal 1
make run-scheduler                             # terminal 2 — separate terminal
cd ../frontend && npm install && npm run dev   # terminal 3
```

The API listens on `http://localhost:8080`, the frontend dev server on `http://localhost:5173`.

### Production (Docker)

```bash
cp .env.example .env      # fill in DB_ROOT_PASSWORD, DB_PASSWORD, REDIS_PASSWORD, DOMAIN, SSL_EMAIL
make prod-build
make prod-up
```

This builds and starts every service — `mysql`, `redis`, `api`, `scheduler`, `frontend`, plus `nginx` and `certbot` for TLS termination — behind a single reverse proxy. See [`docs/oracle-cloud-setup.md`](./docs/oracle-cloud-setup.md) for the full walkthrough, including the DNS and firewall steps and the self-signed-cert bootstrap needed before certbot can issue a real one.

## Project Structure

```
watchtower/
├── backend/                  # Go + Gin API & scheduler — see backend/README.md
├── frontend/                 # Vue.js 3 SPA — see frontend/README.md
├── nginx/                    # Production Nginx config (TLS termination, reverse proxy)
├── docs/                     # Deployment guides
├── docker-compose.yml        # Development stack (mysql, redis, api, scheduler, frontend)
├── docker-compose.prod.yml   # Production overlay (adds nginx + certbot)
├── Makefile                  # dev-up / dev-down / prod-build / prod-up / prod-down / db-migrate
└── .env.example              # Root-level env vars consumed by docker-compose
```

## Documentation

- Backend: [`./backend/README.md`](./backend/README.md)
- Frontend: [`./frontend/README.md`](./frontend/README.md)
- Deployment: [`./docs/oracle-cloud-setup.md`](./docs/oracle-cloud-setup.md)
