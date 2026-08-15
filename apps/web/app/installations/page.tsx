"use client";

import { useCallback, useEffect, useState } from "react";
import { AppShell } from "@/components/AppShell";
import {
  ApiError,
  getMe,
  listOrganizations,
  listRepositories,
  setRepositoryEnabled,
} from "@/lib/api";
import type { Organization, Repository } from "@/lib/types";

type LoadState =
  | { phase: "loading" }
  | { phase: "error"; message: string }
  | {
      phase: "ready";
      organizations: Organization[];
      repositories: Record<string, Repository[]>;
      installUrl: string | null;
    };

export default function Installations() {
  const [state, setState] = useState<LoadState>({ phase: "loading" });

  const load = useCallback(async () => {
    try {
      // The install link comes from /me; a 503 means auth (and therefore
      // /me) is not configured, which never blocks the repository list.
      const [organizations, me] = await Promise.all([
        listOrganizations(),
        getMe().catch(() => null),
      ]);
      const lists = await Promise.all(
        organizations.map((org) =>
          listRepositories({ organizationId: org.id, limit: 200 }),
        ),
      );
      const repositories: Record<string, Repository[]> = {};
      organizations.forEach((org, i) => {
        repositories[org.id] = lists[i];
      });
      setState({
        phase: "ready",
        organizations,
        repositories,
        installUrl: me?.install_url ?? null,
      });
    } catch (err) {
      const message =
        err instanceof ApiError ? err.message : "unexpected failure";
      setState({ phase: "error", message });
    }
  }, []);

  useEffect(() => {
    const initial = setTimeout(() => void load(), 0);
    return () => clearTimeout(initial);
  }, [load]);

  return (
    <AppShell>
      <div className="px-8 py-6">
        <header className="flex items-baseline justify-between gap-6">
          <div>
            <h1 className="text-lg font-semibold">Installations</h1>
            <p className="mt-1 text-sm text-muted">
              GitHub accounts running the Agent Trail app, and which of their
              repositories agents may work on.
            </p>
          </div>
          {state.phase === "ready" &&
            state.installUrl !== null &&
            state.organizations.length > 0 && (
              <a
                href={state.installUrl}
                className="shrink-0 text-sm text-accent underline-offset-4 hover:underline"
              >
                Install on another account
              </a>
            )}
        </header>
        <div className="mt-6">
          {state.phase === "loading" && <InstallationsSkeleton />}
          {state.phase === "error" && (
            <ErrorNotice message={state.message} onRetry={load} />
          )}
          {state.phase === "ready" &&
            (state.organizations.length === 0 ? (
              <Empty installUrl={state.installUrl} />
            ) : (
              <div className="flex flex-col gap-8">
                {state.organizations.map((org) => (
                  <OrganizationSection
                    key={org.id}
                    organization={org}
                    repositories={state.repositories[org.id] ?? []}
                  />
                ))}
              </div>
            ))}
        </div>
      </div>
    </AppShell>
  );
}

function OrganizationSection({
  organization,
  repositories,
}: {
  organization: Organization;
  repositories: Repository[];
}) {
  const [rows, setRows] = useState(repositories);
  const enabled = rows.filter((r) => r.is_enabled).length;
  return (
    <section aria-label={organization.github_account_login}>
      <header className="flex items-baseline justify-between gap-6 border-b border-border pb-2">
        <h2 className="flex items-baseline gap-2 text-base font-semibold">
          <span className="font-mono">{organization.github_account_login}</span>
          <span className="text-sm font-normal text-muted">
            {organization.github_account_type}
          </span>
        </h2>
        <span className="text-sm text-muted">
          {enabled} of {rows.length} enabled
        </span>
      </header>
      {rows.length === 0 ? (
        <p className="px-2 py-6 text-sm text-muted">
          No repositories synced for this account yet.
        </p>
      ) : (
        <ul>
          {rows.map((repository) => (
            <RepositoryRow
              key={repository.id}
              repository={repository}
              onChanged={(updated) =>
                setRows((prev) =>
                  prev.map((r) => (r.id === updated.id ? updated : r)),
                )
              }
            />
          ))}
        </ul>
      )}
    </section>
  );
}

function RepositoryRow({
  repository,
  onChanged,
}: {
  repository: Repository;
  onChanged: (r: Repository) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return (
    <li className="grid grid-cols-[1fr_auto] items-center gap-x-4 border-b border-border px-2 py-2">
      <span className="min-w-0">
        <span className="flex items-baseline gap-2">
          <span className="truncate font-mono text-sm">
            {repository.full_name}
          </span>
          {repository.is_private && (
            <span className="text-sm text-muted">private</span>
          )}
        </span>
        <span className="mt-0.5 block text-sm text-muted">
          {repository.is_enabled
            ? "Enabled: agents may run here"
            : "Disabled: agents will not run here"}
          {error !== "" && <span className="text-danger"> {"- " + error}</span>}
        </span>
      </span>
      <button
        type="button"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          setError("");
          try {
            onChanged(
              await setRepositoryEnabled(repository.id, !repository.is_enabled),
            );
          } catch (err) {
            setError(
              err instanceof ApiError ? err.message : "unexpected failure",
            );
          } finally {
            setBusy(false);
          }
        }}
        className="rounded border border-border px-3 py-1.5 text-sm font-semibold hover:bg-surface disabled:opacity-60"
      >
        {repository.is_enabled ? "Disable" : "Enable"}
      </button>
    </li>
  );
}

function Empty({ installUrl }: { installUrl: string | null }) {
  return (
    <div className="flex justify-center py-16">
      <div className="max-w-sm text-center">
        <p className="text-sm text-muted">
          No GitHub account is connected yet.
        </p>
        {installUrl !== null && (
          <a
            href={installUrl}
            className="mt-4 inline-block text-sm text-accent underline-offset-4 hover:underline"
          >
            Install the GitHub App
          </a>
        )}
      </div>
    </div>
  );
}

function ErrorNotice({
  message,
  onRetry,
}: {
  message: string;
  onRetry: () => void;
}) {
  return (
    <div className="mt-24 flex justify-center">
      <div className="max-w-sm text-center">
        <p className="text-sm text-danger">
          Could not load installations: {message}.
        </p>
        <button
          type="button"
          onClick={onRetry}
          className="mt-4 rounded border border-border px-3 py-1.5 text-sm font-semibold hover:bg-surface"
        >
          Retry
        </button>
      </div>
    </div>
  );
}

function InstallationsSkeleton() {
  return (
    <div aria-hidden className="animate-pulse">
      <div className="h-5 w-44 rounded bg-surface" />
      <div className="mt-3 flex flex-col gap-2">
        {[0, 1, 2].map((row) => (
          <div key={row} className="h-10 rounded bg-surface" />
        ))}
      </div>
    </div>
  );
}
