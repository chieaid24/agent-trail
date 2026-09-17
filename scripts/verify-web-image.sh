#!/usr/bin/env bash
# builds the dashboard image and proves it serves as the non-root user on a read-only root
# filesystem and proxies /backend/* to the API_PROXY_TARGET given at run time
set -euo pipefail

cd "$(dirname "$0")/.."

run_id="$$-$(date +%s)"
IMAGE=${WEB_IMAGE:-agent-trail-web:verify-$run_id}
network="agent-trail-web-verify-$run_id"
stub=""
container=""

cleanup() {
  [ -z "$container" ] || docker rm -f "$container" >/dev/null 2>&1 || true
  [ -z "$stub" ] || docker rm -f "$stub" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
  [ -n "${WEB_IMAGE:-}" ] || docker image rm -f "$IMAGE" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker build -q --provenance=false -f deploy/docker/Dockerfile --target web -t "$IMAGE" . >/dev/null

docker network create "$network" >/dev/null
# stand-in API so the proxy check needs no control plane
stub=$(docker run -d --network "$network" --network-alias api-stub busybox:1.37.0 \
  sh -c 'mkdir -p /www && printf "stub-ok\n" > /www/healthz && exec httpd -f -p 8080 -h /www')

# --read-only mirrors readonlyRootFilesystem in the ECS task definition
container=$(docker run -d --read-only --network "$network" \
  -e API_PROXY_TARGET=http://api-stub:8080 --publish 127.0.0.1::3000 "$IMAGE")

uid=$(docker exec "$container" id -u)
[ "$uid" = "65532" ] || { echo "dashboard runs as uid $uid, expected 65532" >&2; exit 1; }

port=$(docker port "$container" 3000/tcp | head -n1 | sed 's/.*://')
base="http://127.0.0.1:$port"
status() { curl -s --max-time 5 -o /dev/null -w '%{http_code}' "$base$1"; }
fail() { echo "$1" >&2; docker logs "$container" >&2; exit 1; }

for _ in $(seq 1 30); do
  [ "$(status /healthz)" = "200" ] && break
  sleep 1
done
[ "$(status /healthz)" = "200" ] || fail "dashboard /healthz did not return 200 within 30s"
# a real page render proves the standalone bundle and static assets are complete
[ "$(status /login)" = "200" ] || fail "dashboard /login did not return 200"
# the body must come from the stub named only at run time
[ "$(curl -s --max-time 5 "$base/backend/healthz")" = "stub-ok" ] ||
  fail "dashboard did not proxy /backend/healthz to API_PROXY_TARGET"
docker logs "$container" 2>&1 | grep -qE 'EROFS|Error' && fail "dashboard logged errors"

echo "dashboard image ok: /healthz, /login, and /backend proxy as uid $uid on :$port"
