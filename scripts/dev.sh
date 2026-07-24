#!/usr/bin/env bash
# Run api, worker, and web in the foreground with one Ctrl-C teardown.
# Infra (postgres etc.) is expected up already: `make dev` handles both.
# Kills only the PIDs it started; never touches other processes.
set -euo pipefail

cd "$(dirname "$0")/.."

# .env feeds the apps (API_ADDR, DATABASE_URL, ...); compose reads it itself.
if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

pids=()
cleanup() {
  for pid in "${pids[@]:-}"; do
    kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

(cd apps/api && exec go run ./cmd/api) &
pids+=($!)
(cd apps/api && exec go run ./cmd/worker) &
pids+=($!)
(cd apps/web && exec npm run dev --silent) &
pids+=($!)

echo "dev: api, worker, and web running; Ctrl-C stops all three"
wait -n
# One process died; propagate so the trap tears the rest down.
exit 1
