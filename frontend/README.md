# WatchTower Frontend

This is the web client for WatchTower — a Vue 3 single-page app where users manage which crypto/stock/gold assets and security keywords they're tracking, review triggered alerts, and connect their account to the WatchTower Telegram bots.

## Tech Stack

- Vue.js 3 (Composition API)
- Vite
- Naive UI
- Pinia
- Vue Router 4
- vue-i18n (EN/ID)

## Prerequisites

- Node.js 20+
- Backend WatchTower running on port 8080

## Setup & Development

```bash
cd frontend
cp .env.example .env    # adjust values if your backend isn't on localhost:8080
npm install
npm run dev
```

The dev server starts on `http://localhost:5173` and expects the backend API to already be reachable at the URL configured in `VITE_API_BASE_URL`.

## Environment Variables

All variables are read at build time by Vite and must be prefixed with `VITE_`. See `.env.example` for the full list:

| Variable | Description |
|---|---|
| `VITE_API_BASE_URL` | Base URL of the backend API, e.g. `http://localhost:8080/api`. Note there's no `/v1` — the backend doesn't version its routes. |
| `VITE_ASSET_BOT_USERNAME` | Username (without `@`) of the Telegram bot that sends asset price alerts, shown as a deep link on the Telegram setup page. |
| `VITE_SENTINEL_BOT_USERNAME` | Username (without `@`) of the Telegram bot that sends sentinel/security alerts, shown as a deep link on the Telegram setup page. |

## Build for Production

```bash
npm run build
```

Output is written to `dist/` as static assets. In production this is served by the Nginx container defined in `frontend/Dockerfile` and `frontend/nginx.conf`, which also reverse-proxies `/api/` and `/webhook/` requests to the backend so the built app can call same-origin paths.

## Project Structure

```
src/
├── api/            # Axios instance + one module per backend resource (auth, market, notifications, subscriptions)
├── assets/         # Static assets bundled by Vite (images, global styles)
├── components/     # Reusable UI building blocks, grouped by area (layout, market, notifications, common)
├── constants/      # Static lookup data, e.g. the asset symbol options shown in dropdowns
├── locales/        # vue-i18n message files (en.json, id.json)
├── plugins/        # App-wide setup: Naive UI theme/discrete API, vue-i18n instance
├── router/         # Vue Router routes and the auth navigation guard
├── stores/         # Pinia stores (auth, market, notifications, subscriptions)
├── utils/          # Small standalone helpers, e.g. client-side input sanitization/validation
└── views/          # Route-level page components (dashboard, login, register, asset/keyword management, profile, telegram setup, notifications)
```

## Security

- `npm run audit` — runs `npm audit` (SCA) to catch known-vulnerable dependencies
- `npm run lint:security` — runs `retire.js` against `src/` for vulnerable JS library usage
- `npm run test` — runs the Vitest unit test suite
