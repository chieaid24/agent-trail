#!/usr/bin/env bash
# builds the dashboard image and proves it serves as the non-root user on a read-only root filesystem
set -euo pipefail

cd "$(dirname "$0")/.."

IMAGE=${WEB_IMAGE:-agent-trail-web:verify}
container=""

cleanup() {
  [ -z "$container" ] || docker rm -f "$container" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker build -q --provenance=false -f deploy/docker/Dockerfile --target web -t "$IMAGE" . >/dev/null

# --read-only mirrors readonlyRootFilesystem in the ECS task definition
container=$(docker run -d --read-only --publish 127.0.0.1::3000 "$IMAGE")

uid=$(docker exec "$container" id -u)
[ "$uid" = "65532" ] || { echo "dashboard runs as uid $uid, expected 65532" >&2; exit 1; }

port=$(docker port "$container" 3000/tcp | head -n1 | sed 's/.*://')
for _ in $(seq 1 30); do
  if [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/healthz")" = "200" ]; then
    break
  fi
  sleep 1
done
[ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/healthz")" = "200" ] ||
  { echo "dashboard /healthz did not return 200 within 30s" >&2; docker logs "$container" >&2; exit 1; }

# a real page render proves the standalone bundle and static assets are complete
[ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/login")" = "200" ] ||
  { echo "dashboard /login did not return 200" >&2; docker logs "$container" >&2; exit 1; }

if docker logs "$container" 2>&1 | grep -q -i "EROFS\|error"; then
  echo "dashboard logged errors on a read-only root filesystem" >&2
  docker logs "$container" >&2
  exit 1
fi

echo "dashboard image ok: /healthz and /login 200 as uid $uid on :$port"
