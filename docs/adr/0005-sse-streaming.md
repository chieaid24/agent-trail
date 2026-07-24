# ADR-0005: Server-sent events for live streaming

- Status: accepted
- Date: 2026-07-24

## Context

The dashboard streams task timelines and logs live. The data flows one way,
server to browser; the client's only upstream actions (cancel a task,
trigger a run) are ordinary request/response calls. Candidates: server-sent
events, WebSockets, polling.

## Decision

Live updates use server-sent events (SSE) over plain HTTP.

## Alternatives

- WebSockets: bidirectional, but nothing here needs a client-to-server
  stream, and WebSockets buy that unneeded direction at the cost of a
  protocol upgrade, trickier proxies and load balancers, and hand-rolled
  reconnection.
- Polling: simplest possible transport, but live logs at useful latency
  turn into a request storm, and the timeline loses its "events appear as
  they happen" property.

## Consequences

- Reconnection is native: the browser retries automatically and sends
  `Last-Event-ID`, so resume-after-disconnect is a server-side cursor
  lookup, which the dashboard milestone's "SSE reconnects correctly"
  acceptance depends on.
- SSE rides existing HTTP middleware: auth, logging, and correlation IDs
  apply unchanged.
- Browsers cap concurrent HTTP/1.1 connections per origin (about 6); the
  deployment must serve HTTP/2 or the dashboard must multiplex streams.

## Security implications

- The stream authenticates like every other request (session cookie, same
  middleware); no separate socket auth scheme to get wrong.
- Event payloads pass the same redaction rules as stored logs; streaming
  is not a side channel around them.

## Revisit conditions

- A feature needs true bidirectional interaction, such as steering an
  agent mid-run from the dashboard.
- Measured streaming latency or connection limits that SSE over HTTP/2
  cannot meet.
