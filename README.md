# WatchTower

Self-hosted finance and security alerts for Telegram. WatchTower checks crypto, US stock and gold prices against your own rules, and watches vulnerability and security feeds for your keywords. Everything it finds in one check is sent as a single Telegram message.

You clone it, run one setup script, and the whole thing (database, API, web app, scheduler) runs on your own machine. No paid services and no AI.

## What it does

- **Asset alerts.** Price above/below a level and 24h % change, per asset, with a cooldown so the same alert is not repeated every check.
- **Sentinel.** Polls NVD, CISA KEV, GitHub Security Advisories, BleepingComputer, The Hacker News, Exploit-DB and GitHub. Items matching your keywords arrive with CVSS severity, an "actively exploited" flag for CISA KEV entries, and CISA's required action.
- **Check now.** A button on the web app runs your checks immediately instead of waiting for the schedule.
- **Laptop friendly.** The schedule is stored in the database. After your machine wakes from sleep, a missed check runs within a minute, once.

## Architecture

```
┌──────────────┐   /api, /health   ┌─────────────┐      ┌─────────┐
│ Vue 3 web app│──────────────────▶│   Gin API   │─────▶│  MySQL  │
│  (nginx)     │                   └──────┬──────┘      │  Redis  │
└──────────────┘                          │ run queue   └────▲────┘
 127.0.0.1:8080                           ▼ (Redis)          │
                                   ┌─────────────┐           │
                                   │  Scheduler  │───────────┘
                                   │ asset   4h  │
                                   │ sentinel 6h │
                                   └──────┬──────┘
                      ┌───────────────────┼──────────────────┐
                      ▼                   ▼                  ▼
               Market data APIs    Security feeds      Telegram bots
```

## Self-hosting

### Prerequisites

- **Docker:** Docker Desktop on macOS or Windows; Docker Engine with the compose plugin on Linux.
- **Two Telegram bots** from [@BotFather](https://t.me/BotFather): one for asset alerts, one for sentinel. Keep each bot's token and username.
- **Optional:** a free [Twelve Data](https://twelvedata.com) API key (needed for stock alerts) and a GitHub token (raises sentinel's GitHub rate limit).

### Setup

```bash
git clone https://github.com/KarvinNanda/watchtower.git
cd watchtower
```

| OS | Command |
|---|---|
| macOS / Linux | `./scripts/setup.sh` |
| Windows | `powershell -ExecutionPolicy Bypass -File scripts\setup.ps1` |

The script creates `.env` with random database, Redis and JWT secrets, asks for your bot tokens (press Enter to skip and edit `.env` later), builds the images and starts everything. It never overwrites an existing `.env`.

Open http://localhost:8080, register, then link both bots on the **Telegram Setup** page.

### Everyday use

| Action | Command |
|---|---|
| Start | `docker compose up -d` |
| Stop | `docker compose down` |
| Update | `git pull && docker compose up -d --build` |
| Logs | `docker compose logs -f scheduler` |

After editing `.env`, run `docker compose up -d --build` (bot usernames are baked into the web app at build time).

### Security defaults

- Only the web app is published, and only on `127.0.0.1`. MySQL, Redis, the API and the scheduler are reachable only inside the Docker network, so other devices on the same Wi-Fi cannot reach them.
- The JWT lives in an `HttpOnly` cookie. `COOKIE_SECURE=false` is correct for `http://localhost`; set it to `true` only behind HTTPS.
- Secrets are generated from the OS random source and stored only in `.env` (gitignored).

### Tested on

| OS | Status |
|---|---|
| macOS (Apple Silicon) | Full stack |
| Linux (Ubuntu, CI) | Full stack |
| Windows | CI runs only `setup.ps1 -NoStart -SkipPrompts` (secret generation, no BOM/CRLF, no overwrite). The token prompts, the start/health-check path and Docker Desktop itself have not been verified on Windows yet. |

## Development

```bash
cp .env.example .env                    # or ./scripts/setup.sh --no-start
cp backend/.env.example backend/.env    # DB_HOST=127.0.0.1, same DB/Redis passwords as .env
cp frontend/.env.example frontend/.env
make dev-up                             # mysql/redis on 127.0.0.1 (DB_HOST_PORT / REDIS_HOST_PORT)
cd backend && make run-api              # terminal 1, migrations run on start
cd backend && make run-scheduler        # terminal 2
cd frontend && npm install && npm run dev   # terminal 3, http://localhost:5173
```

Tests: `cd backend && go test ./...` and `cd frontend && npm test`.

## Tech stack

| Layer | Technology | Purpose |
|---|---|---|
| Frontend | Vue 3, Naive UI, Pinia | Bilingual (EN/ID) dashboard, dark mode by default |
| API | Go, Gin | REST API, JWT in an httpOnly cookie, run queue endpoints |
| Scheduler | Go | Asset and sentinel pipelines, manual run consumer |
| Database | MySQL 8 | Users, subscriptions, alert state, schedule state, history |
| Cache / queue | Redis 7 | Market price cache, manual run queue, scheduler heartbeat |
| Delivery | Telegram Bot API | Two bots (asset, sentinel), long polling |
| Packaging | Docker Compose, nginx | One command to run everything on one machine |

## Key technical decisions

- **No AI in the pipeline.** Alerts are built from the data the sources already provide (CVSS, CISA KEV status and required action, your own thresholds). Nothing to pay for, and an alert can never be lost because a model call failed.
- **httpOnly cookie for the JWT,** not `localStorage`, so an XSS bug cannot read the session token.
- **Redis queue for "Check now".** The API and scheduler are separate processes. The API checks the scheduler heartbeat, the user's Telegram link and a per-user 5-minute lock, then queues the job; the scheduler runs it under the same lock as scheduled runs, so the two never overlap.
- **Persisted schedule.** A one-minute "is it due?" check against the `scheduler_state` table replaces an in-memory ticker, which loses ticks while a laptop sleeps.
- **golang-migrate.** Numbered, reversible migrations run automatically on every API and scheduler start.
- **Two Telegram bots.** Asset and sentinel alerts can go to different chats, and a leaked token exposes only one stream.

## Known tradeoffs

- Gold prices are scraped from JSON-LD on `harga-emas.org`; if the page changes, gold quotes stop updating until the scraper is fixed.
- Twelve Data's free tier allows 800 calls/day, so the stock symbol list stays small.
- "Check now" for sentinel runs the global sentinel check (deduplication is global), so other users with matching keywords may receive new items at the same time.

## Project structure

```
watchtower/
├── backend/                  # Go API and scheduler — see backend/README.md
├── frontend/                 # Vue 3 web app — see frontend/README.md
├── scripts/                  # setup.sh (macOS/Linux), setup.ps1 (Windows)
├── docker-compose.yml        # Self-hosted stack (only the web app is published, on 127.0.0.1)
├── docker-compose.dev.yml    # Dev overlay: mysql/redis on 127.0.0.1 for go run
├── Makefile                  # dev-up / dev-down / dev-logs / db-migrate
└── .env.example              # All settings for docker compose
```
