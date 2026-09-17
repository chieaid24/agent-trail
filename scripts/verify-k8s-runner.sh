#!/usr/bin/env bash
# verify k8s runner end to end in a per-run kind cluster; KEEP_CLUSTER=1 keeps it
set -euo pipefail

cd "$(dirname "$0")/.."

RUN_ID="${RANDOM}${RANDOM}"
CLUSTER="agent-trail-verify-${RUN_ID}"
RUNNER_IMAGE="agent-trail/runner:verify-${RUN_ID}"
TOOLS_IMAGE="agent-trail/tools:verify-${RUN_ID}"
ARTIFACTS="$(mktemp -d -t agent-trail-k8s-verify-XXXXXX)"
KUBECTL=(kubectl --context "kind-${CLUSTER}")

log() { printf '\n==> %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

for dep in docker kind kubectl curl openssl python3; do
  command -v "$dep" >/dev/null || fail "$dep is required"
done

cleanup() {
  local status=$?
  if [ "${KEEP_CLUSTER:-0}" = "1" ]; then
    printf 'keeping cluster %s (KEEP_CLUSTER=1)\n' "$CLUSTER"
  else
    kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
  fi
  docker rmi "$RUNNER_IMAGE" "$TOOLS_IMAGE" >/dev/null 2>&1 || true
  if [ $status -ne 0 ]; then
    printf 'artifacts (logs, pod spec): %s\n' "$ARTIFACTS"
  else
    rm -rf "$ARTIFACTS"
  fi
  exit $status
}
trap cleanup EXIT

render() {
  local file=$1
  shift
  local content
  content="$(cat "$file")"
  local pair
  for pair in "$@"; do
    local key=${pair%%=*}
    local value=${pair#*=}
    content=${content//"\${$key}"/"$value"}
  done
  printf '%s\n' "$content"
}

log "Building runner and tools images"
# --provenance=false: kind load rejects manifest lists from provenance attestations
docker build -q --provenance=false -f deploy/docker/Dockerfile --target runner -t "$RUNNER_IMAGE" . >/dev/null
docker build -q --provenance=false -f deploy/docker/Dockerfile --target tools -t "$TOOLS_IMAGE" . >/dev/null

log "Creating kind cluster ${CLUSTER}"
kind create cluster --name "$CLUSTER" --wait 180s >/dev/null
# archive route: kind load docker-image fails on containerd-store multi-platform indexes
IMAGES_TAR="$(mktemp -t agent-trail-images-XXXXXX.tar)"
docker save "$RUNNER_IMAGE" "$TOOLS_IMAGE" -o "$IMAGES_TAR"
kind load image-archive --name "$CLUSTER" "$IMAGES_TAR" >/dev/null
rm -f "$IMAGES_TAR"

log "Applying namespaces, service accounts, and network policies"
"${KUBECTL[@]}" apply -f deploy/k8s/local/namespace.yaml -f deploy/k8s/runner/namespace.yaml >/dev/null
"${KUBECTL[@]}" apply -f deploy/k8s/runner/serviceaccount.yaml -f deploy/k8s/local/networkpolicy.yaml >/dev/null
KUBERNETES_API_IP="$("${KUBECTL[@]}" -n default get service kubernetes -o jsonpath='{.spec.clusterIP}')"
render deploy/k8s/runner/networkpolicy.yaml \
  "KUBERNETES_API_CIDR=${KUBERNETES_API_IP}/32" \
  | "${KUBECTL[@]}" apply -f - >/dev/null

log "Creating per-run secrets"
PGPW="$(openssl rand -hex 16)"
DB_URL="postgres://agent_trail:${PGPW}@postgres.agent-trail-local.svc.cluster.local:5432/agent_trail?sslmode=disable"
KEY_PEM="$(openssl genrsa 2048 2>/dev/null)"
"${KUBECTL[@]}" -n agent-trail-local create secret generic local-postgres \
  --from-literal=password="$PGPW" >/dev/null
"${KUBECTL[@]}" -n agent-trail-local create secret generic local-database \
  --from-literal=url="$DB_URL" >/dev/null
"${KUBECTL[@]}" -n agent-trail-runners create secret generic runner-database \
  --from-literal=url="$DB_URL" >/dev/null
"${KUBECTL[@]}" -n agent-trail-runners create secret generic runner-github \
  --from-literal=webhook-secret="fixture-webhook-secret" \
  --from-literal=app-id="1" \
  --from-literal=key.pem="$KEY_PEM" >/dev/null

log "Starting postgres"
"${KUBECTL[@]}" apply -f deploy/k8s/local/postgres.yaml >/dev/null
"${KUBECTL[@]}" -n agent-trail-local rollout status deployment/postgres --timeout=300s >/dev/null

log "Running migrations"
render deploy/k8s/local/migrate-job.yaml "TOOLS_IMAGE=$TOOLS_IMAGE" | "${KUBECTL[@]}" apply -f - >/dev/null
"${KUBECTL[@]}" -n agent-trail-local wait --for=condition=complete job/migrate --timeout=180s >/dev/null

log "Starting the GitHub fixture (seeds one queued task)"
render deploy/k8s/local/fixture.yaml "TOOLS_IMAGE=$TOOLS_IMAGE" | "${KUBECTL[@]}" apply -f - >/dev/null
"${KUBECTL[@]}" -n agent-trail-local rollout status deployment/fixture --timeout=180s >/dev/null

log "Starting the Kubernetes runner controller"
render deploy/k8s/runner/controller.yaml \
  "RUNNER_IMAGE=$RUNNER_IMAGE" \
  "TTL_SECONDS=20" \
  "WORKER_IDLE_EXIT_SECONDS=120" \
  "AGENT_PROVIDER=fake" \
  "AGENT_CLI_PATH=claude" \
  "AGENT_MODEL=fake-model" \
  "AGENT_PERMISSION_MODE=acceptEdits" \
  "AGENT_CLI_VERSION=unused" \
  "CONFLICT_LLM_ENABLED=false" \
  "CONFLICT_LLM_PROVIDER=fake" \
  "CONFLICT_LLM_MODEL=claude-sonnet-4-6" \
  "GITHUB_API_BASE_URL=http://fixture.agent-trail-local.svc.cluster.local:8080" \
  | sed 's|cloudwatch-agent-collector.amazon-cloudwatch.svc.cluster.local:4317|off|' \
  | "${KUBECTL[@]}" apply -f - >/dev/null
"${KUBECTL[@]}" -n agent-trail-runners rollout status deployment/runner-controller --timeout=180s >/dev/null

log "Verifying least-privilege controller RBAC"
CONTROLLER_USER="system:serviceaccount:agent-trail-runners:runner-controller"
for verb in create get list watch delete; do
  [ "$("${KUBECTL[@]}" auth can-i "$verb" jobs.batch -n agent-trail-runners --as="$CONTROLLER_USER")" = "yes" ] \
    || fail "runner-controller cannot $verb Jobs"
done
for resource in secrets pods serviceaccounts roles.rbac.authorization.k8s.io; do
  [ "$("${KUBECTL[@]}" auth can-i get "$resource" -n agent-trail-runners --as="$CONTROLLER_USER")" = "no" ] \
    || fail "runner-controller can read $resource"
done

log "Waiting for the controller-created runner Job"
JOB_NAME=""
for _ in $(seq 1 60); do
  JOB_NAME="$("${KUBECTL[@]}" -n agent-trail-runners get jobs \
    -l app.kubernetes.io/managed-by=agent-trail-controller \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
  [ -n "$JOB_NAME" ] && break
  sleep 2
done
[ -n "$JOB_NAME" ] || fail "runner controller did not create a Job"

log "Asserting the pod spec carries the hardening"
for _ in $(seq 1 60); do
  POD="$("${KUBECTL[@]}" -n agent-trail-runners get pods -l job-name="$JOB_NAME" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
  [ -n "$POD" ] && break
  sleep 2
done
[ -n "${POD:-}" ] || fail "runner pod never appeared"
"${KUBECTL[@]}" -n agent-trail-runners get pod "$POD" -o json > "$ARTIFACTS/runner-pod.json"
"${KUBECTL[@]}" -n agent-trail-runners get "job/$JOB_NAME" -o json > "$ARTIFACTS/runner-job.json"
"${KUBECTL[@]}" get namespace agent-trail-runners -o json > "$ARTIFACTS/runner-namespace.json"
python3 - "$ARTIFACTS/runner-pod.json" "$ARTIFACTS/runner-job.json" "$ARTIFACTS/runner-namespace.json" <<'PY'
import json, sys
pod = json.load(open(sys.argv[1]))
job = json.load(open(sys.argv[2]))
ns = json.load(open(sys.argv[3]))
spec = pod["spec"]
sec = spec["securityContext"]
c = spec["containers"][0]
csec = c["securityContext"]
env = {item["name"]: item.get("value") for item in c.get("env", [])}
volumes = {v["name"]: v for v in spec.get("volumes", [])}
checks = {
    "automountServiceAccountToken is false": spec.get("automountServiceAccountToken") is False,
    "service account runner-task": spec.get("serviceAccountName") == "runner-task",
    "runAsNonRoot": sec.get("runAsNonRoot") is True,
    "runAsUser 65532": sec.get("runAsUser") == 65532,
    "fsGroup 65532": sec.get("fsGroup") == 65532,
    "seccomp RuntimeDefault": sec.get("seccompProfile", {}).get("type") == "RuntimeDefault",
    "readOnlyRootFilesystem": csec.get("readOnlyRootFilesystem") is True,
    "no privilege escalation": csec.get("allowPrivilegeEscalation") is False,
    "capabilities drop ALL": csec.get("capabilities", {}).get("drop") == ["ALL"],
    "OTLP endpoint is explicit": env.get("OTEL_EXPORTER_OTLP_ENDPOINT") == "off",
    "restartPolicy Never": spec.get("restartPolicy") == "Never",
    "activeDeadlineSeconds set": job["spec"].get("activeDeadlineSeconds", 0) > 0,
    "backoffLimit 0": job["spec"].get("backoffLimit") == 0,
    "no docker socket": all(
        (v.get("hostPath") or {}).get("path", "") == "" for v in volumes.values()
    ),
    "no service account token volume": all(
        "kube-api-access" not in name for name in volumes
    ),
    "workspace emptyDir size-limited": bool(
        (volumes.get("workspace", {}).get("emptyDir") or {}).get("sizeLimit")
    ),
    "tmp emptyDir size-limited": bool(
        (volumes.get("tmp", {}).get("emptyDir") or {}).get("sizeLimit")
    ),
    "cpu limit": c["resources"]["limits"]["cpu"] == "2",
    "memory limit": c["resources"]["limits"]["memory"] == "4Gi",
    "namespace enforces restricted PSA": ns["metadata"]["labels"].get(
        "pod-security.kubernetes.io/enforce") == "restricted",
}
bad = [name for name, ok in checks.items() if not ok]
for name in checks:
    print(("  ok  " if name not in bad else "  BAD ") + name)
if bad:
    sys.exit("pod spec is missing hardening: " + ", ".join(bad))
PY

log "Waiting for the task to complete inside the Job"
"${KUBECTL[@]}" -n agent-trail-runners wait --for=condition=complete "job/$JOB_NAME" --timeout=300s >/dev/null
"${KUBECTL[@]}" -n agent-trail-runners logs "$POD" > "$ARTIFACTS/runner-pod.log" 2>&1 || true
"${KUBECTL[@]}" -n agent-trail-runners logs deployment/runner-controller > "$ARTIFACTS/runner-controller.log" 2>&1 || true

log "Verifying the task outcome through the fixture"
"${KUBECTL[@]}" -n agent-trail-local port-forward svc/fixture :8080 > "$ARTIFACTS/port-forward.log" 2>&1 &
PF_PID=$!
FIXTURE_PORT=""
for _ in $(seq 1 40); do
  FIXTURE_PORT="$(sed -n 's/^Forwarding from 127.0.0.1:\([0-9]*\).*/\1/p' "$ARTIFACTS/port-forward.log" | head -1)"
  [ -n "$FIXTURE_PORT" ] && break
  sleep 0.5
done
[ -n "$FIXTURE_PORT" ] || { kill "$PF_PID" 2>/dev/null || true; fail "port-forward never came up"; }
VERIFY="$(curl -fsS "http://127.0.0.1:${FIXTURE_PORT}/verify")"
kill "$PF_PID" 2>/dev/null || true
printf '%s\n' "$VERIFY" > "$ARTIFACTS/verify.json"
printf '  %s\n' "$VERIFY"
python3 - "$ARTIFACTS/verify.json" <<'PY'
import json, sys
v = json.load(open(sys.argv[1]))
if v.get("task_status") != "awaiting_review":
    sys.exit(f"task_status = {v.get('task_status')!r}, want awaiting_review")
if v.get("pr_open") is not True:
    sys.exit("no draft pull request was opened")
PY

log "Verifying TTL cleanup removes the finished Job"
TTL_GONE=0
for _ in $(seq 1 60); do
  if ! "${KUBECTL[@]}" -n agent-trail-runners get "job/$JOB_NAME" >/dev/null 2>&1; then
    TTL_GONE=1
    break
  fi
  sleep 2
done
[ "$TTL_GONE" = "1" ] || fail "Job still present after TTL"
echo "  ok  Job removed by ttlSecondsAfterFinished"

log "PASS: controller created the restricted Job, task completed, TTL cleaned up"
printf 'artifacts: %s\n' "$ARTIFACTS"
