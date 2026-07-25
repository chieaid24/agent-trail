# ADR-0006: Standard-library GitHub client, no SDK

- Status: accepted
- Date: 2026-07-25

## Context

The control plane calls a small, stable slice of the GitHub REST API as a
GitHub App: mint an app JWT, exchange it for installation tokens, list
installation repositories, read collaborator permission and branch heads,
post issue comments, create check runs. The obvious shortcut is
google/go-github plus a JWT library; the cost is a large dependency in the
one component that holds the app's private key.

## Decision

`internal/github` implements the client on the standard library: RS256 app
JWTs via crypto/rsa, installation-token caching with an expiry margin, and
hand-written request wrappers for the handful of endpoints the integration
uses.

## Alternatives

- google/go-github: complete coverage and typed responses, but it is a
  very large surface for six endpoints, moves at GitHub's pace rather than
  ours, and adds a third-party dependency to the credential-holding
  component. Rejected while the endpoint count stays single-digit.
- golang-jwt for the app JWT: the token is one fixed header and three
  claims; crypto/rsa plus base64url covers it in a page of audited code.

## Consequences

- Each new GitHub endpoint is a small hand-written wrapper with its own
  test; there is no generated client to lean on.
- Response parsing is limited to the fields the integration reads, so
  GitHub-side additions cannot break decoding.
- The dependency tree of the credential-holding service stays flat
  (pgx and goose remain the only substantial third-party code).

## Security implications

- The app private key is only touched by stdlib crypto; no third-party
  code sees it.
- Responses are size-capped and request bodies never logged, enforced in
  one place (`Client.do`).

## Revisit conditions

- The endpoint count grows past a dozen wrappers, or the integration
  needs GraphQL, pagination-heavy listings, or webhook-type generation
  where a maintained SDK pays for its weight.
