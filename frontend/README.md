# WatchTower Frontend

A Vue.js 3 single-page app for managing WatchTower subscriptions — built with Naive UI, dark mode enabled by default, and fully bilingual (Indonesian/English) via vue-i18n.

## Tech Stack

| Package | Version |
|---|---|
| vue | ^3.5.0 |
| vue-router | ^4.4.0 |
| pinia | ^2.2.0 |
| naive-ui | ^2.40.0 |
| vue-i18n | ^9.14.0 |
| axios | ^1.7.0 |
| @vueuse/core | ^11.0.0 |
| vite | ^6.4.3 |
| vitest | ^4.1.9 |

## Project Structure

```
src/
├── api/            # Axios instance + one module per backend resource (auth, market, notifications, subscriptions)
├── assets/         # Static assets bundled by Vite (images, global styles)
├── components/     # Reusable UI building blocks, grouped by area (layout, market, notifications, common)
├── constants/      # Static lookup data — the asset symbol options shown in dropdowns
├── locales/        # vue-i18n message files (en.json, id.json)
├── plugins/        # App-wide setup — Naive UI theme/discrete API, vue-i18n instance
├── router/         # Vue Router routes and the session-restoring navigation guard
├── stores/         # Pinia stores (auth, market, notifications, subscriptions)
├── utils/          # Client-side input sanitization/validation helpers
└── views/          # Route-level page components
```

## Pages

| Page | Description |
|---|---|
| Login | Email/password sign-in; on success the backend sets the httpOnly session cookie |
| Register | Account creation with client-side email format and password strength validation |
| Dashboard | Asset/keyword subscription counts, last alert timestamps, market snapshot, recent notifications |
| Asset Subscriptions | Manage tracked crypto/stock/gold symbols and their price/percentage alert thresholds |
| Keyword Subscriptions | Manage the security keywords matched against incoming sentinel items |
| Profile | Devices, OS list, expertise level, per-bot Telegram chat IDs, alert cooldown, preferred language |
| Notifications | Paginated history of delivered asset and sentinel alerts |
| Telegram Setup | Step-by-step guide to connect both the asset and sentinel Telegram bots to the account |

## Environment Variables

All variables are read at build time by Vite and must be prefixed with `VITE_`:

| Variable | Description |
|---|---|
| `VITE_API_BASE_URL` | Base URL of the backend API, e.g. `http://localhost:8080/api` — no `/v1`, the backend doesn't version its routes |
| `VITE_ASSET_BOT_USERNAME` | Username (without `@`) of the Telegram bot that sends asset price alerts, used for the deep link on the Telegram setup page |
| `VITE_SENTINEL_BOT_USERNAME` | Username (without `@`) of the Telegram bot that sends sentinel/security alerts, used for the deep link on the Telegram setup page |

## Development

```bash
npm install
npm run dev
npm run build
npm run test
npm run audit
```

## Security

- JWT delivered via an httpOnly cookie — no token in `localStorage` or any JavaScript-accessible storage
- Input sanitization (`src/utils/security.js`) before values are sent in API calls
- CSP headers set by Nginx in production
- `withCredentials: true` on the shared Axios instance, required for the browser to send/accept the auth cookie
- Cookie `SameSite` is `Lax` in development and `Strict` in production, set server-side alongside `Secure`

## Known Issues

- The main JS bundle (`index-*.js`) is ~636 kB before gzip, past Vite's 500 kB soft-warning threshold — functional, but code-splitting via `manualChunks` or dynamic `import()` for the heavier views (Naive UI's `Select`, `Pagination`, `Input` chunks are the biggest contributors) hasn't been done yet.
- A moderate Vite/esbuild dev-server advisory was previously flagged by `npm audit` (esbuild's dev server could be reached by any local site). It's been resolved by upgrading to `vite@6.4.3` — `npm audit` currently reports 0 vulnerabilities.
