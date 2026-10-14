# Uses the `docker compose` v2 CLI plugin, not the deprecated standalone
# `docker-compose` v1 binary.
COMPOSE := docker compose
DEV_FILES := -f docker-compose.yml -f docker-compose.dev.yml

.PHONY: dev-up dev-down dev-logs db-migrate

# Starts only mysql/redis, published on 127.0.0.1, for `go run` / `npm run dev`.
dev-up:
	$(COMPOSE) $(DEV_FILES) up -d mysql redis

dev-down:
	$(COMPOSE) $(DEV_FILES) down

dev-logs:
	$(COMPOSE) $(DEV_FILES) logs -f

# Optional: migrations already run automatically on every api/scheduler
# start (see internal/db.DB.RunMigrations). Requires Go on the host.
db-migrate:
	cd backend && make migrate-up
