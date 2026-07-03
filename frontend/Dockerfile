# syntax=docker/dockerfile:1

# ---- Builder ----
FROM node:20-alpine AS builder

WORKDIR /app

COPY package*.json ./
RUN npm ci

COPY . .

# Vite bakes every VITE_* variable into the built bundle at build time, not
# at container runtime. Defaulting to relative paths here means the built
# app calls same-origin /api/... and /webhook/..., which nginx.conf (below)
# proxies to the api service — no rebuild needed if the domain changes.
# Override only if the frontend is ever served from a different origin
# than the API, e.g.:
#   docker compose build --build-arg VITE_API_BASE_URL=https://api.example.com/api frontend
ARG VITE_API_BASE_URL=/api
ARG VITE_ASSET_BOT_USERNAME=WatchTowerAssetBot
ARG VITE_SENTINEL_BOT_USERNAME=WatchTowerSentinelBot
ENV VITE_API_BASE_URL=${VITE_API_BASE_URL} \
    VITE_ASSET_BOT_USERNAME=${VITE_ASSET_BOT_USERNAME} \
    VITE_SENTINEL_BOT_USERNAME=${VITE_SENTINEL_BOT_USERNAME}

RUN npm run build

# ---- Runtime ----
FROM nginx:alpine

COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
