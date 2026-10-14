# Uses the `docker compose` v2 CLI plugin, not the deprecated standalone
# `docker-compose` v1 binary.
COMPOSE := docker compose

.PHONY: dev-up dev-down dev-logs db-migrate

# Starts only mysql/redis, for running the api/scheduler binaries locally
# with `go run` against a containerized database during development.
dev-up:
	$(COMPOSE) up -d mysql redis

dev-down:
	$(COMPOSE) down

dev-logs:
	$(COMPOSE) logs -f

# Optional: migrations already run automatically on every api/scheduler
# start (see internal/db.DB.RunMigrations). Requires Go on the host.
db-migrate:
	cd backend && make migrate-up
