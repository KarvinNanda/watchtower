# Uses the `docker compose` v2 CLI plugin (installed via the
# docker-compose-plugin package per docs/oracle-cloud-setup.md), not the
# deprecated standalone `docker-compose` v1 binary the two commands are
# not interchangeable, and the v1 binary is no longer packaged on current
# Ubuntu.
COMPOSE := docker compose
PROD_FILES := -f docker-compose.yml -f docker-compose.prod.yml

.PHONY: dev-up dev-down dev-logs prod-build prod-up prod-down db-migrate

# Starts only mysql/redis, for running the api/scheduler binaries locally
# with `go run` against a containerized database during development.
dev-up:
	$(COMPOSE) up -d mysql redis

dev-down:
	$(COMPOSE) down

dev-logs:
	$(COMPOSE) logs -f

prod-build:
	$(COMPOSE) $(PROD_FILES) build

prod-up:
	$(COMPOSE) $(PROD_FILES) up -d

prod-down:
	$(COMPOSE) $(PROD_FILES) down

# Optional: migrations already run automatically on every api/scheduler
# container start (see internal/db.DB.RunMigrations, called from both
# cmd/api and cmd/scheduler). This target is only useful for running
# migrations manually from the host — e.g. before the app containers exist
# — and requires Go to be installed on the host, since it shells out to
# `go run` (see backend/Makefile's migrate-up).
db-migrate:
	cd backend && make migrate-up
