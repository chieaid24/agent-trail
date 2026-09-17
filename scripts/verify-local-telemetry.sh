#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

allocate_ports() {
  node <<'NODE'
const net = require("node:net");

(async () => {
  const servers = [];
  for (let i = 0; i < 6; i += 1) {
    const server = net.createServer();
    await new Promise((resolve, reject) => {
      server.once("error", reject);
      server.listen(0, "127.0.0.1", resolve);
    });
    servers.push(server);
  }
  for (const server of servers) console.log(server.address().port);
  await Promise.all(servers.map((server) => new Promise((resolve) => server.close(resolve))));
})().catch((error) => {
  console.error(error);
  process.exit(1);
});
NODE
}

mapfile -t available_ports < <(allocate_ports)

project_prefix=${COMPOSE_PROJECT_NAME:-agent-trail-telemetry}
project="$project_prefix-$(date +%s%N)-$$"
export COMPOSE_PROJECT_NAME=$project
export POSTGRES_PORT=${POSTGRES_PORT:-${available_ports[0]}}
export GRAFANA_PORT=${GRAFANA_PORT:-${available_ports[1]}}
export OTLP_GRPC_PORT=${OTLP_GRPC_PORT:-${available_ports[2]}}
export OTLP_HTTP_PORT=${OTLP_HTTP_PORT:-${available_ports[3]}}
api_port=${TELEMETRY_API_PORT:-${available_ports[4]}}
fixture_port=${TELEMETRY_FIXTURE_PORT:-${available_ports[5]}}

evidence=${TELEMETRY_EVIDENCE_DIR:-$ROOT/artifacts/local-telemetry-$project}
mkdir -p "$evidence"
run_dir=$(mktemp -d)
pids=()

compose() {
  docker compose -p "$project" "$@"
}

stop_processes() {
  local pid
  for pid in "${pids[@]:-}"; do
    if kill -0 "$pid" 2>/dev/null; then
      kill -TERM "$pid" 2>/dev/null || true
    fi
  done
  for pid in "${pids[@]:-}"; do
    wait "$pid" 2>/dev/null || true
  done
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  stop_processes
  compose logs --no-color otel-lgtm >"$evidence/otel-lgtm.log" 2>&1 || true
  compose down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$run_dir"
  if [ "$status" -ne 0 ]; then
    printf 'telemetry smoke failed; evidence: %s\n' "$evidence" >&2
  fi
  exit "$status"
}
trap cleanup EXIT INT TERM

wait_for_url() {
  local name=$1
  local url=$2
  local deadline=$((SECONDS + 90))
  until curl -fsS "$url" >/dev/null 2>&1; do
    if [ "$SECONDS" -ge "$deadline" ]; then
      printf '%s did not become ready: %s\n' "$name" "$url" >&2
      return 1
    fi
    sleep 1
  done
}

database_url="postgres://agent_trail:agent_trail@127.0.0.1:$POSTGRES_PORT/agent_trail?sslmode=disable"
otel_endpoint="127.0.0.1:$OTLP_GRPC_PORT"
grafana_url="http://127.0.0.1:$GRAFANA_PORT"
fixture_url="http://127.0.0.1:$fixture_port"
api_url="http://127.0.0.1:$api_port"

compose up -d --wait postgres otel-lgtm
wait_for_url Grafana "$grafana_url/api/health"

mkdir -p "$run_dir/bin" "$run_dir/workspaces"
(cd apps/api && go build -o "$run_dir/bin/migrate" ./cmd/migrate)
(cd apps/api && go build -o "$run_dir/bin/api" ./cmd/api)
(cd apps/api && go build -o "$run_dir/bin/worker" ./cmd/worker)
(cd apps/api && go build -o "$run_dir/bin/fixture-github" ./cmd/fixture-github)

DATABASE_URL=$database_url "$run_dir/bin/migrate" up >"$evidence/migrate.log" 2>&1
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 \
  -out "$run_dir/github-app.pem" >/dev/null 2>&1

DATABASE_URL=$database_url \
FIXTURE_ADDR="127.0.0.1:$fixture_port" \
FIXTURE_PUBLIC_URL=$fixture_url \
  "$run_dir/bin/fixture-github" >"$evidence/fixture.log" 2>&1 &
fixture_pid=$!
pids+=("$fixture_pid")
wait_for_url fixture "$fixture_url/healthz"

DATABASE_URL=$database_url \
API_ADDR="127.0.0.1:$api_port" \
OTEL_EXPORTER_OTLP_ENDPOINT=$otel_endpoint \
GITHUB_WEBHOOK_SECRET=telemetry-smoke-secret \
GITHUB_APP_ID=1 \
GITHUB_APP_PRIVATE_KEY_PATH="$run_dir/github-app.pem" \
GITHUB_API_BASE_URL=$fixture_url \
  "$run_dir/bin/api" >"$evidence/api.log" 2>&1 &
api_pid=$!
pids+=("$api_pid")
wait_for_url API "$api_url/readyz"

webhook_status=$(curl -sS -o "$evidence/webhook-response.json" -w '%{http_code}' \
  -X POST -H 'X-Hub-Signature-256: sha256=invalid' -d '{}' \
  "$api_url/webhooks/github")
if [ "$webhook_status" != 401 ]; then
  printf 'invalid webhook returned %s, want 401\n' "$webhook_status" >&2
  exit 1
fi

DATABASE_URL=$database_url \
OTEL_EXPORTER_OTLP_ENDPOINT=$otel_endpoint \
GITHUB_WEBHOOK_SECRET=telemetry-smoke-secret \
GITHUB_APP_ID=1 \
GITHUB_APP_PRIVATE_KEY_PATH="$run_dir/github-app.pem" \
GITHUB_API_BASE_URL=$fixture_url \
WORKSPACE_ROOT="$run_dir/workspaces" \
AGENT_PROVIDER=fake \
WORKER_MAX_TASKS=1 \
WORKER_IDLE_EXIT_SECONDS=30 \
  "$run_dir/bin/worker" >"$evidence/worker.log" 2>&1 &
worker_pid=$!
pids+=("$worker_pid")
wait "$worker_pid"

curl -fsS "$fixture_url/verify" -o "$evidence/task.json"
node -e '
const fs=require("node:fs");
const result=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));
if(result.task_status!=="awaiting_review"||result.pr_open!==true)process.exit(1);
' "$evidence/task.json"
task_id=$(node -e '
const fs=require("node:fs");
const result=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));
process.stdout.write(result.task_id||"");
' "$evidence/task.json")
if [ -z "$task_id" ]; then
  printf 'fixture verification returned no task ID\n' >&2
  exit 1
fi

kill -TERM "$api_pid"
wait "$api_pid"

curl -fsS -u admin:admin "$grafana_url/api/datasources" \
  -o "$evidence/datasources.json"
prometheus_uid=$(node -e '
const fs=require("node:fs");
const sources=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));
process.stdout.write(sources.find((source)=>source.type==="prometheus")?.uid||"");
' "$evidence/datasources.json")
tempo_uid=$(node -e '
const fs=require("node:fs");
const sources=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));
process.stdout.write(sources.find((source)=>source.type==="tempo")?.uid||"");
' "$evidence/datasources.json")
if [ -z "$prometheus_uid" ] || [ -z "$tempo_uid" ]; then
  printf 'Grafana did not provision Prometheus and Tempo data sources\n' >&2
  exit 1
fi

metric_query='agent_trail_webhook_invalid_signature_total'
trace_query="{ resource.service.name = \"agent-trail-worker\" && span.task.id = \"$task_id\" }"

query_metric() {
  local output=$1
  curl -fsS -u admin:admin -G \
    "$grafana_url/api/datasources/proxy/uid/$prometheus_uid/api/v1/query" \
    --data-urlencode "query=$metric_query" -o "$output" &&
    node -e '
const fs=require("node:fs");
const response=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));
if(response.status!=="success"||response.data.result.length===0)process.exit(1);
' "$output"
}

query_trace() {
  local output=$1
  curl -fsS -u admin:admin -G \
    "$grafana_url/api/datasources/proxy/uid/$tempo_uid/api/search" \
    --data-urlencode "q=$trace_query" -o "$output" &&
    node -e '
const fs=require("node:fs");
const response=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));
if(!Array.isArray(response.traces)||response.traces.length===0)process.exit(1);
' "$output"
}

wait_for_query() {
  local name=$1
  shift
  local deadline=$((SECONDS + 90))
  until "$@"; do
    if [ "$SECONDS" -ge "$deadline" ]; then
      printf '%s did not become queryable\n' "$name" >&2
      return 1
    fi
    sleep 2
  done
}

wait_for_query metric query_metric "$evidence/metric-before-restart.json"
wait_for_query trace query_trace "$evidence/trace-before-restart.json"

compose restart otel-lgtm >/dev/null
wait_for_url Grafana "$grafana_url/api/health"
wait_for_query persisted-metric query_metric "$evidence/metric-after-restart.json"
wait_for_query persisted-trace query_trace "$evidence/trace-after-restart.json"

cat >"$evidence/summary.txt" <<EOF
Compose project: $project
Grafana URL: $grafana_url
OTLP gRPC endpoint: $otel_endpoint
PromQL: $metric_query
TraceQL: $trace_query
Services: agent-trail-api, agent-trail-worker
Task: $task_id (awaiting_review with draft PR)
Persistence: metric and trace found after otel-lgtm restart
Application logs: stdout only; no OTLP log exporter is configured
EOF

cat "$evidence/summary.txt"
printf 'Evidence: %s\n' "$evidence"
