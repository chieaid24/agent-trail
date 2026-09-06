SHELL := /usr/bin/env bash

# .env included, not exported wholesale, so tests stay hermetic
-include .env

DATABASE_URL ?= postgres://agent_trail:agent_trail@localhost:5432/agent_trail?sslmode=disable
TEST_DATABASE_URL ?= $(DATABASE_URL)

.PHONY: dev infra migrate seed test integration-test e2e bench slice clean hooks

dev: infra migrate
	bash scripts/dev.sh

infra:
	docker compose up -d --wait

migrate:
	cd apps/api && DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate up

seed:
	cd apps/api && DATABASE_URL="$(DATABASE_URL)" go run ./cmd/seed

test:
	cd apps/api && go test ./...
	cd apps/web && npm test --silent

integration-test:
	cd apps/api && TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test ./...

e2e:
	cd apps/web && npx playwright test

bench:
	bash scripts/bench.sh

slice:
	cd apps/api && DATABASE_URL="$(DATABASE_URL)" go run ./cmd/slice

clean:
	docker compose down -v --remove-orphans
	rm -rf apps/web/.next apps/web/node_modules/.cache

hooks:
	git config core.hooksPath .githooks
	@echo "pre-commit hook installed (core.hooksPath -> .githooks)"
