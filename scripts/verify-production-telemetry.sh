#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

CONFIG=deploy/terraform/modules/control_plane/cloudwatch-agent.json
IMAGE="public.ecr.aws/cloudwatch-agent/cloudwatch-agent:1.300071.0b1720-amd64@sha256:f5680928b37cd5afacb5fd3de1b7ef7bfba57976f4f8b4227b49615467460cf2"
VALIDATION_CONFIG=$(mktemp -t agent-trail-cloudwatch-agent-XXXXXX.json)
CONTAINER="agent-trail-cloudwatch-agent-verify-$$"

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  rm -f "$VALIDATION_CONFIG"
}
trap cleanup EXIT

python3 - "$CONFIG" "$VALIDATION_CONFIG" <<'PY'
import json
import sys

source, destination = sys.argv[1:]
with open(source, encoding="utf-8") as handle:
    config = json.load(handle)

otlp = config["opentelemetry"]["collect"]["otlp"]
expected = {
    "grpc_endpoint": "0.0.0.0:4317",
    "http_endpoint": "0.0.0.0:4318",
}
if otlp != expected:
    raise SystemExit(f"OTLP receiver = {otlp!r}, want {expected!r}")

config["agent"]["region"] = "us-east-1"
with open(destination, "w", encoding="utf-8") as handle:
    json.dump(config, handle)
PY

docker run --rm --name "$CONTAINER" --platform linux/amd64 \
  --entrypoint /opt/aws/amazon-cloudwatch-agent/bin/config-translator \
  --mount "type=bind,src=$VALIDATION_CONFIG,dst=/config/cloudwatch-agent.json,readonly" \
  "$IMAGE" \
  -input /config/cloudwatch-agent.json \
  -output /tmp/cloudwatch-agent.toml \
  -mode onPremise \
  -os linux

docker run --rm --name "$CONTAINER" --platform linux/amd64 \
  --env CW_CONFIG_CONTENT="$(cat "$CONFIG")" \
  --env AWS_REGION=us-east-1 \
  --entrypoint /opt/aws/amazon-cloudwatch-agent/bin/config-translator \
  "$IMAGE" \
  -output /tmp/health.toml \
  -mode auto \
  -os linux

printf 'PASS: CloudWatch agent accepted the production OTLP configuration\n'
