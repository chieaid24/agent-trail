# ADR-0002: Go for the control plane

- Status: accepted
- Date: 2026-07-24

## Context

The control plane is a long-lived concurrent server: it terminates webhooks,
runs a scheduler loop, controls runners, and streams events to the dashboard.
The realistic candidates were Go and NestJS (TypeScript), with the dashboard
already fixed on Next.js/TypeScript.

## Decision

The control plane (api, worker, migrate) is written in Go on the standard
library HTTP stack, with pgx for PostgreSQL and goose for migrations.

## Alternatives

- NestJS: one language across backend and frontend, decorator-driven
  structure. Loses on the runtime: the control plane is mostly concurrent
  I/O and process supervision, where goroutines and contexts are simpler
  than an event loop plus worker threads, and a static binary deploys into
  a minimal runner image without a node_modules tree.
- Rust: strongest runtime guarantees, rejected on iteration speed for a
  system whose shape is still moving milestone to milestone.

## Consequences

- The repo carries two languages; CI gates both (gofmt/vet/test/build and
  prettier/eslint/vitest/next build).
- API contracts between control plane and dashboard need explicit schemas
  rather than shared types; packages/contracts owns that when it lands.
- Single static binaries keep runner and deploy images small.

## Security implications

- Go is memory-safe, and the stdlib-first approach keeps the dependency
  tree small and auditable; all dependencies are version-pinned.
- One less runtime (no JS on the control plane) in the attack surface of
  the component that holds GitHub credentials.

## Revisit conditions

- The contracts between web and api grow so entangled that end-to-end
  TypeScript would remove a class of drift bugs.
- A milestone needs a library ecosystem Go lacks.
