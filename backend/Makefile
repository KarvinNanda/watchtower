-include .env
export

MIGRATE_VERSION := v4.17.1
MIGRATIONS_PATH := migrations
DB_DSN := mysql://$(DB_USER):$(DB_PASSWORD)@tcp($(DB_HOST):$(DB_PORT))/$(DB_NAME)?charset=utf8mb4&collation=utf8mb4_unicode_ci&parseTime=true
MIGRATE := go run -tags 'mysql' github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)

.PHONY: run-api run-scheduler migrate-up migrate-down migrate-status build tidy install-tools sca security test test-coverage test-race

run-api:
	go run ./cmd/api

run-scheduler:
	go run ./cmd/scheduler

migrate-up:
	$(MIGRATE) -path $(MIGRATIONS_PATH) -database "$(DB_DSN)" up

migrate-down:
	$(MIGRATE) -path $(MIGRATIONS_PATH) -database "$(DB_DSN)" down 1

migrate-status:
	$(MIGRATE) -path $(MIGRATIONS_PATH) -database "$(DB_DSN)" version

build:
	go build -o bin/api ./cmd/api
	go build -o bin/scheduler ./cmd/scheduler

tidy:
	go mod tidy

test:
	go test ./... -v -count=1

test-coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

test-race:
	go test ./... -race -count=1

# Install the Go official vulnerability scanner and gosec static analyzer.
install-tools:
	go install golang.org/x/vuln/cmd/govulncheck@latest
	go install github.com/securego/gosec/v2/cmd/gosec@latest

# Run static Software Composition Analysis: known-vulnerable dependencies
# (govulncheck) and common Go security anti-patterns (gosec).
sca:
	@echo "=== Running govulncheck ==="
	govulncheck ./...
	@echo "=== Running gosec ==="
	gosec -fmt=json -out=security-report.json ./...
	@echo "=== SCA complete. Check security-report.json ==="

# Run every security check, including a dependency vulnerability sweep via
# Sonatype Nancy against the OSS Index database. Requires Docker.
security: sca
	@echo "=== Checking for known vulnerabilities ==="
	go list -json -m all | \
		docker run --rm -i \
		sonatypecommunity/nancy:latest sleuth
