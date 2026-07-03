# WatchTower

Personal finance & security intelligence platform with AI-powered Telegram alerts.

## What is WatchTower?

WatchTower watches crypto, stock, and gold prices alongside CVE and security news for keywords you care about, then sends you a plain-language summary the moment something worth your attention happens. Every alert is analyzed by DeepSeek AI before it's sent, so you get context instead of raw numbers, and delivery goes straight to Telegram. It's built as a multi-user SaaS — every subscriber configures their own assets, keywords, and Telegram chat independently.

## Architecture Overview

```
  [Vue.js Frontend] ←→ [Gin API] ←→ [MySQL]
                              ↕           ↕
                    [Scheduler]    [Redis Cache]
                         ↕
                   [DeepSeek AI]
                         ↕
                 [Telegram Bots]
```

## Services

| Service | Tech | Port |
|---------|------|------|
| Frontend | Vue.js 3 + Nginx | 80 |
| API | Go + Gin | 8080 |
| Scheduler | Go | - |
| Database | MySQL 8 | 3306 |
| Cache | Redis 7 | 6379 |

## Quick Start (Development)

1. Clone repo
2. Set up `.env` (see `.env.example` at the root, and `backend/.env.example` for the API/scheduler config)
3. Start DB & Redis: `make dev-up`
4. `cd backend && make run-api`
5. `cd backend && make run-scheduler` (separate terminal)
6. `cd frontend && npm run dev`

## Documentation

- Backend: [`./backend/README.md`](./backend/README.md)
- Frontend: [`./frontend/README.md`](./frontend/README.md)
- Deployment: [`./docs/oracle-cloud-setup.md`](./docs/oracle-cloud-setup.md)

## Project Structure

```
watchtower/
├── backend/              # Go + Gin API & Scheduler
├── frontend/             # Vue.js 3 SPA
├── nginx/                # Production Nginx config
├── docs/                 # Deployment guides
├── docker-compose.yml    # Development stack (mysql, redis, api, scheduler, frontend)
├── docker-compose.prod.yml  # Production overlay (adds nginx + certbot, TLS termination)
├── Makefile              # dev-up/dev-down/prod-build/prod-up/prod-down/db-migrate
└── .env.example          # Root-level env vars consumed by docker-compose
```
