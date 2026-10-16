# WatchTower

Self-hosted finance and security alerts for Telegram. WatchTower checks crypto, US stock and gold prices against your own rules, and watches vulnerability and security feeds for your keywords. Everything it finds in one check is sent as a single Telegram message.

You clone it, run one setup script, and the whole thing (database, API, web app, scheduler) runs on your own machine. No paid services and no AI.

## What it does

- **Asset alerts.** Rules per symbol (20 crypto, 20 US stocks plus the SPY S&P 500 ETF, ANTAM gold): a price level above/below, in USD or in rupiah, optionally labelled buy target, take profit or stop loss; a 24h % change; a trailing stop; and a 52-week high/low break. Each card in the web app shows every rule's level and how far the price is from it. A cooldown keeps the same alert from repeating every check.
- **Status report.** A button sends one Telegram message with every symbol you track, its price and the distance to each rule, even when nothing triggered.
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

### Getting the tokens, keys and chat IDs

Everything below goes into `.env` in the project root (the setup script creates it and asks for the two bot tokens). `.env` is gitignored: treat every value in it like a password and never commit, paste or screenshot it.

| What | Required | `.env` variable | Where it comes from |
|---|---|---|---|
| Asset bot token | Yes | `TELEGRAM_ASSET_BOT_TOKEN` | @BotFather, step 1 below |
| Sentinel bot token | Yes | `TELEGRAM_SENTINEL_BOT_TOKEN` | @BotFather, step 1 below |
| Bot usernames | Yes | `VITE_ASSET_BOT_USERNAME`, `VITE_SENTINEL_BOT_USERNAME` | @BotFather, step 1 below (without the `@`) |
| Chat IDs | Yes | none: saved per user in the web app | The bots themselves, step 2 below |
| Twelve Data API key | For stock alerts | `TWELVE_DATA_API_KEY` | Step 3 below |
| CoinGecko Demo key | No | `COINGECKO_API_KEY` | Step 4 below |
| GitHub token | No | `GITHUB_TOKEN` | Step 5 below |
| USD/IDR rate | No key needed | none | open.er-api.com, free and keyless |

**1. Telegram bot tokens and usernames.** WatchTower uses two bots, so asset and security alerts can go to different chats and a leaked token exposes only one stream. For each bot:

1. In Telegram, open [@BotFather](https://t.me/BotFather) and send `/newbot`.
2. Send a display name (for example `WatchTower Assets`), then a username that ends in `bot` (for example `my_watchtower_assets_bot`).
3. BotFather replies with a token like `123456789:AAH...`. Put it in `TELEGRAM_ASSET_BOT_TOKEN` (or `TELEGRAM_SENTINEL_BOT_TOKEN` for the second bot) and the username, without the `@`, in `VITE_ASSET_BOT_USERNAME` (or `VITE_SENTINEL_BOT_USERNAME`).
4. Repeat for the second bot, then run `docker compose up -d --build` so the web app picks up the usernames.

If a token leaks, send `/revoke` to @BotFather, pick the bot, and put the new token in `.env`. Each token can be used by only one running WatchTower at a time (`TELEGRAM_MODE=polling`); a second copy using the same token makes the bot stop answering.

**2. Telegram chat IDs.** You do not look these up yourself; the bots tell you, and each user saves their own in the web app:

1. Start WatchTower, open the web app, register and log in.
2. Open the **Telegram Setup** page and click **Open Bot in Telegram** for the asset bot (or search for its username in Telegram).
3. Press **Start** or send `/start`. The bot replies with "Chat ID kamu: `123456789`".
4. Copy that number into **Asset Bot Chat ID** on the **Profile** page and save.
5. Repeat with the sentinel bot for **Sentinel Bot Chat ID**. The Telegram Setup page shows both as connected once they are saved.

In a private chat the chat ID is your Telegram user ID, so it is the same number on both bots. You still have to press **Start** on both bots: a bot cannot message someone who never started it.

Alerts can also go to a group. Add the bot to the group, then pick `/start` for that bot from the group's command menu (Telegram sends it as `/start@YourBotName`, the only form a bot in privacy mode receives). The bot replies with "Chat ID grup ini: `-1001234567890`". Group chat IDs are negative; paste that number into the Profile field. Each bot needs its own `/start` in the group, and each field can point to a different chat (for example asset alerts to you, sentinel alerts to a team group).

If the bot does not reply to `/start`, check that the API is running (`docker compose ps`; the API is the process that answers `/start`) and that the token in `.env` belongs to that bot.

**3. Twelve Data API key (stock alerts).** Sign up at [twelvedata.com](https://twelvedata.com), open the API Keys section of your dashboard and copy the key into `TWELVE_DATA_API_KEY`. The free tier allows 800 calls a day and 8 a minute; WatchTower paces its calls for that (see *Known tradeoffs*). Without a key, crypto and gold alerts still work, stock alerts do not.

**4. CoinGecko Demo key (optional).** Crypto prices work without a key through CoinGecko's keyless public API (roughly 10 to 30 calls a minute, depending on load). If you track many crypto symbols, create a free Demo key in the [CoinGecko developer dashboard](https://www.coingecko.com/en/developers/dashboard) and put it in `COINGECKO_API_KEY`. The Demo plan allows 100 calls a minute and 10,000 a month.

**5. GitHub token (optional).** The sentinel searches public GitHub data, which works without a token at 10 requests a minute. A token raises that to 30. Create a [fine-grained personal access token](https://github.com/settings/personal-access-tokens/new) with access to public repositories only and no extra permissions, and put it in `GITHUB_TOKEN`. Give it an expiry date, and revoke it on GitHub if it leaks.

After changing `.env`, run `docker compose up -d --build`.

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
| Database | MySQL 8 | Users, asset rules and their state, keywords, schedule state, history |
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
- Asset alerts are information only, not financial advice. Buy, take-profit and stop-loss wording only repeats the labels you gave your own rules.
- IDR amounts in Telegram messages use the free open.er-api.com USD/IDR rate, which updates once a day. Each message shows the rate and when it was updated. Price levels entered in rupiah and ANTAM trailing stops are evaluated on the rupiah price; everything else is evaluated in USD. For crypto and stocks the rupiah price is the USD price at that daily rate, so it can differ from a local exchange. If no real rate is available, IDR amounts are hidden, rupiah rules wait for the next check and ANTAM is skipped for that run.
- All timestamps in Telegram messages use one timezone per installation (`APP_TIMEZONE`, default `Asia/Jakarta`).
- "Check now" refreshes prices older than 15 minutes. Stock refreshes spend a daily share of the Twelve Data free tier (`TWELVE_DATA_MANUAL_DAILY_CREDITS`, default 400 of 800); when it is used up, Check now uses cached prices and says so. Periodic runs need about `stock symbols × 24 / ASSET_SCHEDULER_INTERVAL_HOURS` credits a day on top of that, so keep the sum under 800. If the provider's daily quota still runs out, stock prices are not retried until 00:00 UTC. The count only covers this installation, so sharing the API key elsewhere uses the same quota.
- Trailing stops also count the source's recent high (about 24 h for crypto, the trading session for stocks), but only highs whose window starts after the rule was created or re-armed: about 25 h later for crypto, the next session for stocks. ANTAM has no recent-high source, so its trailing stop only sees the price at each check (in rupiah).
- `MAX_UNIQUE_SYMBOLS` (default 100) counts symbols across all users of an installation, because they share one price-data quota.

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
