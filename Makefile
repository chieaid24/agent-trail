# Agent Trail development entrypoints. See docs/operations/local-development.md.
SHELL := /usr/bin/env bash

# .env overrides the defaults below (ports, DATABASE_URL, ...). Compose and
# scripts/dev.sh read .env themselves; nothing is blanket-exported, so tests
# stay hermetic.
-include .env

DATABASE_URL ?= postgres://agent_trail:agent_trail@localhost:5432/agent_trail?sslmode=disable
TEST_DATABASE_URL ?= $(DATABASE_URL)

.PHONY: dev infra migrate seed test integration-test e2e demo clean hooks

## dev: start infra (compose), run migrations, run api + worker + web
dev: infra migrate
	bash scripts/dev.sh

## infra: start the compose dev infrastructure and wait for health
infra:
	docker compose up -d --wait

## migrate: apply database migrations
migrate:
	cd apps/api && DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate up

## seed: load demo tasks (skips when the database already has tasks)
seed:
	cd apps/api && DATABASE_URL="$(DATABASE_URL)" go run ./cmd/seed

## test: unit tests for both apps
test:
	cd apps/api && go test ./...
	cd apps/web && npm test --silent

## integration-test: unit tests plus the suites needing a real database
integration-test:
	cd apps/api && TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test ./...

## e2e: browser end-to-end suite against a dedicated disposable stack
## (compose project agent-trail-e2e; ports overridable via E2E_* envs)
e2e:
	cd apps/web && npx playwright test

## demo: scripted issue-to-PR demo against a simulated GitHub (needs infra)
demo:
	cd apps/api && DATABASE_URL="$(DATABASE_URL)" go run ./cmd/demo

## clean: stop infra, drop volumes, remove build artifacts
clean:
	docker compose down -v --remove-orphans
	rm -rf apps/web/.next apps/web/node_modules/.cache

## hooks: install the git pre-commit hook (mirrors the CI gate)
hooks:
	git config core.hooksPath .githooks
	@echo "pre-commit hook installed (core.hooksPath -> .githooks)"
