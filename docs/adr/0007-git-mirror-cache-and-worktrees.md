# ADR-0007: Git mirror cache and worktrees

- Status: accepted
- Date: 2026-07-26

## Context

Every task attempt needs a writable checkout of the target repository, pinned
to a known base commit, that an agent can edit and that the platform can commit,
diff, push, and then discard. Attempts run concurrently, so their checkouts must
be isolated. The push surface is security-sensitive: an agent must never push to
a protected branch, push outside its namespace, or force-push over history. And
every git invocation runs on inputs the agent or the repository can influence,
so none of it may pass through a shell.

## Decision

`internal/gitworkspace` keeps a bare `--mirror` cache per repository at
`WORKSPACE_ROOT/repos/<repository-id>/repo.git` and cuts one worktree per
attempt at `WORKSPACE_ROOT/workspaces/<task-attempt-id>` on a sanitized branch
under the `agent-trail/` prefix, pinned to a base commit that is verified to
exist before checkout. Push policy (branch prefix, `origin` only, no force) is
enforced in Go before git runs. Commits carry Agent-Trail provenance trailers
under a fixed bot identity set per invocation with `-c`. Every git call uses an
argument array with a hardened environment; clone URLs are never logged and are
redacted from error output.

## Alternatives

- A full clone per attempt with no shared cache: simpler, but re-fetches the
  whole repository on every attempt. Rejected on bandwidth and latency once a
  repository sees repeated tasks; a shared mirror fetches only new objects.
- Rely on GitHub branch protection alone for the push guard: it is the user's
  setting, not ours, and does not stop a force-push to an arbitrary ref on a
  repository that lacks it. The code-side guard is the control we own; branch
  protection stays as defense in depth.
- `git interpret-trailers` or a commit template for provenance: heavier than
  joining a body and a trailer block as two `-m` values.
- A cross-process file lock (flock) for the mirror cache now: unnecessary while
  a single runner process owns a host. Deferred until a deployment shares one
  on-disk cache across processes.

## Consequences

- Concurrent attempts on one repository share the mirror; a per-repository lock
  serializes clone and fetch, so worktree creation for a hot repository is
  briefly serialized while isolation across attempts is preserved.
- Cleanup is git-aware: `worktree remove` drops the checkout and `worktree
  prune` only clears entries whose directories are already gone, so it never
  removes a live checkout.
- Because a `--mirror` clone sets `remote.origin.mirror=true`, the flag is
  disabled per push so an explicit-refspec push cannot be reinterpreted as a
  mirror push that prunes upstream refs.
- The runner still provisions a throwaway temp directory today; wiring this
  package into the executor lands with the real agent adapter, when an attempt
  carries a repository clone URL and a base SHA.

## Security implications

- The push guard is the primary control stopping an agent from reaching a
  protected branch or rewriting history. It runs before git, on an argument
  array, so no shell ever interprets agent-influenced input.
- Repository and attempt identifiers are validated as single path components,
  and a created worktree is re-checked through symlink resolution, so a
  checkout (and boundary checks on paths inside it) cannot escape
  `WORKSPACE_ROOT`.
- Clone URLs may embed an installation token; they are never logged and are
  stripped from git error output.
- Limitation: the fetch lock is process-local. A host running more than one
  process against one mirror cache would need a cross-process lock, which is
  not yet built.

## Revisit conditions

- More than one process per host shares the mirror cache (needs file locks).
- The push policy needs to become declarative or policy-engine driven rather
  than a fixed code guard.
- Repositories grow large enough that even a shared mirror's disk footprint
  forces a partial-clone or blobless strategy.
