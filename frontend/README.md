# WatchTower Frontend

A Vue 3 single-page app for WatchTower: asset rules with live distance to each rule, sentinel keywords, notification history and Telegram setup. Built with Naive UI and `@lucide/vue` icons, dark mode by default, fully bilingual (Indonesian/English) via vue-i18n.

## Tech stack

| Package | Version |
|---|---|
| vue | ^3.5.43 |
| vue-router | ^4.4.0 |
| pinia | ^2.2.0 |
| naive-ui | ^2.40.0 |
| @lucide/vue | 1.49.0 |
| vue-i18n | ^9.14.0 |
| axios | ^1.20.0 |
| @vueuse/core | ^11.0.0 |
| vite | ^6.4.3 |
| vitest | ^4.1.11 |

## Project structure

```
src/
├── api/            # Axios instance + one module per backend area (auth, market, notifications, runs, subscriptions = asset rules + keywords)
├── assets/         # Global styles bundled by Vite
├── components/     # assets (SymbolRuleCard), runs (RunNowButton, RunResultPanel), layout, market, notifications, common
├── composables/    # useRunNow: start a manual run and poll its result
├── constants/      # The asset symbol options shown in the rule form
├── locales/        # vue-i18n message files (en.json, id.json); every key exists in both
├── plugins/        # Naive UI theme and discrete API, vue-i18n instance, Naive locale
├── router/         # Routes and the session-restoring navigation guard
├── stores/         # Pinia stores (auth, market, notifications, subscriptions, userData)
├── theme/          # Design tokens (light/dark), CSS variables, Naive UI overrides
├── utils/          # Pure helpers: rule form/payload/validation, rule distance, run results, time formatting, input sanitizing
└── views/          # Route-level pages
```

## Pages

| Page | Description |
|---|---|
| Login | Email/password sign-in; on success the backend sets the httpOnly session cookie |
| Register | Account creation with email and password-strength validation |
| Dashboard | Rule and keyword counts, last alert times, market snapshot, recent notifications |
| Asset Alerts | One card per symbol: price, 24h change, data age, and each rule with its level and distance. Add/edit/pause/delete rules (price level in USD or IDR, 24h change, trailing stop, 52-week break), re-arm a fired trailing stop, **Check now** and **Send report** |
| Sentinel | Manage the keywords matched against security feeds; run the sentinel check now |
| Notifications | Paginated history of delivered alerts and reports |
| Profile | Telegram chat IDs for both bots, alert cooldown, language, devices/OS/expertise |
| Telegram Setup | Step-by-step guide to link both bots, with a check that each chat ID is saved |

## Environment variables

Read at build time by Vite; all must start with `VITE_`:

| Variable | Description |
|---|---|
| `VITE_API_BASE_URL` | Base URL of the backend API, e.g. `http://localhost:8080/api` (the backend has no `/v1`) |
| `VITE_ASSET_BOT_USERNAME` | Username (without `@`) of the asset bot, used for the link on the Telegram Setup page |
| `VITE_SENTINEL_BOT_USERNAME` | Username (without `@`) of the sentinel bot |

See the root README for how to create the bots.

## Development

```bash
npm ci                # install exactly what package-lock.json pins
npm run dev           # http://localhost:5173
npm run build
npm test              # vitest
npm run audit         # npm audit --audit-level=moderate
```

When adding or updating a dependency, write the lockfile with npm 11 (`npx -y npm@11 install ...`): npm 10 strips the lockfile's `libc` fields.

## Tests

Vitest covers the pure helpers (rule form and payload, distance to each rule type, run results, time formatting, sanitizing), the run poller, the theme tokens and contrast, locale parity between `en` and `id`, and two guard tests: no colour literal or emoji in `.vue` files, and no emoji in locale strings.

## Security

- JWT in an httpOnly cookie: no token in `localStorage` or any JavaScript-readable storage
- `withCredentials: true` on the shared Axios instance so the browser sends the auth cookie
- Input sanitizing (`src/utils/security.js`) before values are sent
- Content-Security-Policy and other headers set by nginx (`nginx.conf`) in the Docker image
- Cookie `SameSite` is `Lax` by default and `Strict` when the backend runs with `COOKIE_SECURE=true` (which also sets `Secure`)
- Users only see market data for symbols they have rules for; stores are cleared on logout

## Known issues

- The main JS bundle (`index-*.js`) is about 680 kB before gzip, over Vite's 500 kB warning. It works, but it is not code-split yet (`manualChunks` or more dynamic imports).
