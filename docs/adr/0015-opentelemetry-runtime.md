# ADR-0015: OpenTelemetry runtime with Prometheus and OTLP export

- Status: accepted
- Date: 2026-08-19

## Context

The API already exposed a small process-local Prometheus counter registry, but
runtime observability now needs labelled counters, histograms, resource gauges,
and traces from both the API and worker. Local Grafana needs live metrics while
operators still depend on the API's existing `/metrics` contract.

## Decision

Use the OpenTelemetry Go SDK as the shared instrumentation layer. The API keeps
a Prometheus pull reader and both binaries may add an OTLP/gRPC push reader plus
trace exporter. Local Compose sends OTLP metrics through the collector to
Prometheus and sends traces to the collector's debug exporter.

Metric names omit OTel units and exporter-added suffixes so the documented
Prometheus names remain stable. OTLP is best effort: export failures are logged,
and task execution continues. An explicit `off` endpoint disables push export.

## Alternatives

- Prometheus client metrics plus a separate tracing SDK would preserve familiar
  metric APIs but create two instrumentation lifecycles and duplicate resource
  identity.
- OTLP-only metrics would simplify the binaries but break the existing API
  `/metrics` contract and make local diagnosis depend on a running collector.
- A local trace database would make traces searchable but adds another service
  before trace retention and query requirements exist.

## Consequences

- Metrics share one typed registry and can be read locally or exported.
- The worker has no HTTP metrics server; its runtime metrics require OTLP.
- Linux workers expose process CPU, resident memory, and workspace filesystem
  usage. Other platforms log that resource sampling is unavailable.
- Local traces are visible in collector logs but are not retained or queryable.
- Kubernetes Jobs must receive a reachable collector address or explicitly set
  export to `off`; they must not silently rely on the native-process default.

## Security implications

- Local OTLP is plaintext and binds only to localhost on the host side. A cloud
  deployment must provide authenticated transport and constrain collector
  egress before enabling export.
- Labels are bounded enums or internal failure codes. Repository names, task
  titles, prompts, credentials, and other unbounded or sensitive values are not
  metric labels.
- The API `/metrics` endpoint remains unauthenticated and belongs behind the
  internal network boundary.

## Revisit conditions

- A production collector is deployed: add authenticated transport and verify
  Kubernetes NetworkPolicy and cloud security-group paths.
- Operators need trace search or retention: choose a trace backend and define
  retention before enabling it.
- Prometheus pull compatibility is no longer required: consider OTLP-only
  metrics for both binaries.
