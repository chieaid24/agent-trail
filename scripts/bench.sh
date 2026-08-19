#!/usr/bin/env bash
# Reproducible benchmark and failure-injection run (issue: benchmarks and
# failure injection; results in docs/testing/benchmark-results.md).
#
# Boots a dedicated Postgres (its own compose project and port, so parallel
# dev stacks and lanes are untouched), migrates it, then runs the gated
# tests in apps/api/internal/bench with AGENT_TRAIL_BENCH=1. The full-disk
# injection additionally mounts a 1 MiB tmpfs and fills it, when
# passwordless sudo is available; without it that one test skips.
#
#   scripts/bench.sh                 # full run
#   BENCH_RUN=TestScheduler scripts/bench.sh   # one benchmark
#   BENCH_KEEP=1 scripts/bench.sh    # keep the database up afterwards
#
# Ports/names are overridable for parallel lanes:
#   BENCH_PROJECT (default agent-trail-bench)
#   BENCH_POSTGRES_PORT (default 5493)
set -euo pipefail

cd "$(dirname "$0")/.."

PROJECT="${BENCH_PROJECT:-agent-trail-bench}"
PG_PORT="${BENCH_POSTGRES_PORT:-5493}"
DB_URL="postgres://agent_trail:agent_trail@127.0.0.1:${PG_PORT}/agent_trail?sslmode=disable"
CONTAINER="${PROJECT}-postgres-1"
RUN_FILTER="${BENCH_RUN:-.}"
KEEP="${BENCH_KEEP:-0}"
ARTIFACTS="apps/api/internal/bench/.artifacts"

FULL_DISK_DIR=""

log() { printf 'bench: %s\n' "$*"; }

cleanup() {
  if [ -n "$FULL_DISK_DIR" ]; then
    sudo -n umount "$FULL_DISK_DIR" 2>/dev/null || true
    rmdir "$FULL_DISK_DIR" 2>/dev/null || true
  fi
  if [ "$KEEP" = "1" ]; then
    log "BENCH_KEEP=1: leaving $CONTAINER up (db at $DB_URL)"
    return
  fi
  docker compose -p "$PROJECT" down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

log "starting postgres (project $PROJECT, port $PG_PORT)"
POSTGRES_PORT="$PG_PORT" docker compose -p "$PROJECT" up -d --wait postgres

log "migrating"
(cd apps/api && DATABASE_URL="$DB_URL" go run ./cmd/migrate up)

# Full-disk injection: a 1 MiB tmpfs filled to ENOSPC. Best effort - the
# mount needs passwordless sudo; without it TestInjectFullDisk skips.
if sudo -n true 2>/dev/null; then
  FULL_DISK_DIR="$(mktemp -d /tmp/agent-trail-bench-fulldisk.XXXXXX)"
  sudo -n mount -t tmpfs -o size=1m,mode=0777 tmpfs "$FULL_DISK_DIR"
  dd if=/dev/zero of="$FULL_DISK_DIR/fill" bs=4096 2>/dev/null || true
  log "full-disk tmpfs mounted at $FULL_DISK_DIR"
else
  log "no passwordless sudo: TestInjectFullDisk will skip"
fi

mkdir -p "$ARTIFACTS"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
LOG_FILE="$ARTIFACTS/bench-$STAMP.log"

log "running benchmarks (filter: $RUN_FILTER; log: $LOG_FILE)"
(
  cd apps/api
  env AGENT_TRAIL_BENCH=1 \
      TEST_DATABASE_URL="$DB_URL" \
      BENCH_PG_CONTAINER="$CONTAINER" \
      ${FULL_DISK_DIR:+BENCH_FULL_DISK_DIR="$FULL_DISK_DIR"} \
      go test ./internal/bench/ -count=1 -v -timeout 30m -run "$RUN_FILTER"
) | tee "$LOG_FILE"

log "done; measured output in $LOG_FILE"
