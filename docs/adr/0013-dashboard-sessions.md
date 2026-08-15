# ADR-0013: Dashboard sessions - GitHub OAuth with database-backed cookies

- Status: accepted
- Date: 2026-08-15

## Context

The task and dashboard endpoints were unauthenticated by design while the
API bound to localhost. Before any deployment exposes them, the dashboard
needs a login, and the system overview already constrains the shape: GitHub
OAuth, GitHub App installation, secure HTTP-only session cookies, and no
custom email/password authentication. The SSE stream adds a hard
requirement: EventSource cannot set request headers, so whatever carries
the session must be a cookie. The threat model requires resource requests
to verify organization membership on the server.

## Decision

Sign in with the GitHub App's OAuth user authorization (web application
flow), implemented on the standard library per ADR-0006.

- Sessions are rows: a 256-bit random bearer token, stored only as its
  SHA-256 hash, joined to `users`, with a fixed 30-day expiry and no
  sliding refresh. Logout deletes the row; expired rows are reaped
  opportunistically on the next login.
- The cookie (`agent_trail_session`) is HttpOnly, SameSite=Lax, Path=/,
  Secure when `AUTH_COOKIE_SECURE` is set. The browser reaches the API only
  through the dashboard's same-origin `/backend` proxy, so the cookie stays
  first-party and no CORS surface exists.
- The OAuth `state` is a random value bound to the browser in a short-lived
  HttpOnly cookie and compared in constant time on the callback.
- Enforcement is config-gated: with `GITHUB_OAUTH_CLIENT_ID` and
  `GITHUB_OAUTH_CLIENT_SECRET` set (all-or-none validated), every `/api/v1`
  route and `/me` require a live session; without them the auth endpoints
  answer 503 and the API keeps its documented localhost-development
  openness. `/healthz`, `/readyz`, `/metrics`, `/webhooks/github`, and the
  auth flow itself never require a session.
- Membership is resynced at every login from `GET /user/installations`
  using the user token: one `memberships` row per account that has a synced
  organization. Repository enablement writes require membership in the
  repository's organization. A failed sync logs and keeps the previous
  memberships rather than failing the login.

## Alternatives

- **Stateless JWT sessions**: no DB read per request, but revocation
  (logout, compromised token) needs a denylist that reintroduces the
  database, plus key management. Rejected; one indexed lookup is cheap.
- **NextAuth / an auth library in the web app**: moves identity into the
  Next.js process, but the Go API is the enforcement point and the SSE
  stream terminates there. The API would still need its own session check.
  Rejected to keep one authority.
- **Storing raw session tokens**: simpler debugging, but a database leak
  would hand out live sessions. Hashing costs one SHA-256.
- **Deriving the OAuth redirect URI from request headers**: works behind
  well-configured proxies, but trusts Host/X-Forwarded-Host, a known
  redirect-poisoning surface. Rejected for an explicit
  `AUTH_PUBLIC_ORIGIN`.

## Consequences

- Every authenticated request costs one indexed session lookup.
- The dashboard origin is configuration (`AUTH_PUBLIC_ORIGIN`), so the
  GitHub App needs one registered callback URL per origin
  (`<origin>/backend/auth/github/callback`).
- Sessions expire hard at 30 days; users sign in again.
- Roles exist in the schema (`owner|admin|member|viewer`) but every synced
  membership is written as `member` for now; GitHub does not expose a role
  on `GET /user/installations`. Finer role mapping is future work.

## Security implications

- Task and dashboard endpoints reject unauthenticated requests once OAuth
  credentials are configured; deployments must set them (and
  `AUTH_COOKIE_SECURE=true` behind HTTPS).
- CSRF: SameSite=Lax blocks cross-site cookie sends on POST; the state
  cookie binds the OAuth callback to the initiating browser. There is no
  per-request CSRF token; revisit if any endpoint ever accepts form
  encodings or safe-method side effects.
- Authorization inside the API is membership-based only; it satisfies the
  threat model's organization-membership check, not yet its role check.
- Enablement changes are audit-logged as structured events
  (`repository_enablement_changed` with actor and trace id), not yet
  written to a durable audit table.

## Revisit conditions

- Any deployment beyond a single trusted operator (role enforcement,
  durable audit log).
- Session volume where per-request lookups or table growth measurably
  hurt; add sliding expiry or a cache then.
- GitHub deprecating the OAuth endpoints used here.
