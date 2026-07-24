# ADR-0001: Augment GitHub instead of hosting repositories

- Status: accepted
- Date: 2026-07-24

## Context

Agent Trail needs a place where issues are filed, pull requests are reviewed,
and merges happen. Building that surface ourselves means rebuilding repository
hosting, permissions, code review, and merge queues - none of which is the
product. The product is the control plane between an issue and a reviewed PR:
task state, isolation, observability, validation, evidence.

## Decision

Agent Trail integrates with GitHub through a GitHub App and never hosts
repositories or rebuilds review. GitHub stays the collaboration surface;
Agent Trail consumes webhooks and produces draft PRs, check runs, and
comments.

## Alternatives

- Self-hosted forge (Gitea, GitLab CE) under our control: full API freedom,
  but the team using Agent Trail does not live there, and migrating them is a
  bigger ask than installing an app.
- Own PR and review UI on top of raw git: rebuilds the hardest commodity
  feature of GitHub for zero differentiation.

Both lose to the same argument: every hour spent on hosting is an hour not
spent on the observability surface that is the actual value.

## Consequences

- The GitHub App boundary (webhooks in, REST/GraphQL out) is the single
  integration point; docs/architecture/github-app.md specifies it.
- Agent Trail inherits GitHub's availability and API rate limits; the
  control plane must budget and monitor GitHub API usage.
- Users keep their existing review workflow; human approval of the merge
  stays on GitHub, which VISION.md requires anyway.

## Security implications

- Webhook signatures must be validated before any payload is trusted.
- Installation tokens are scoped to the repositories the user enabled; the
  blast radius of a leaked token is bounded by the installation.
- Agent Trail never holds long-lived user credentials; it exchanges App keys
  for short-lived installation tokens.

## Revisit conditions

- A second forge (GitLab, Bitbucket) becomes a real user requirement.
- GitHub API rate limits or product changes block a core flow.
