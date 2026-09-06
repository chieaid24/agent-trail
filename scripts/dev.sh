#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

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
exit 1
