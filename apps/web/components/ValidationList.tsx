import { TrustBadge } from "./TrustBadge";
import { formatDuration } from "@/lib/format";
import type { ValidationResult, ValidationStatus } from "@/lib/types";

const STATUS_CLASSES: Record<ValidationStatus, string> = {
  passed: "text-success",
  failed: "text-danger",
  timed_out: "text-warning",
  error: "text-warning",
};

// Trusted validation results. Each result is a self-contained unit
// (DESIGN.md allows a card here); the trust badge is the load-bearing part.
export function ValidationList({ results }: { results: ValidationResult[] }) {
  if (results.length === 0) {
    return (
      <p className="py-8 text-sm text-muted">
        No validation results yet. Checks run after the agent finishes editing.
      </p>
    );
  }

  const attempts = [...new Set(results.map((r) => r.attempt_number))];

  return (
    <div className="mt-2 flex flex-col gap-6">
      {attempts.map((attempt) => (
        <section key={attempt} aria-label={`Attempt ${attempt} validations`}>
          {attempts.length > 1 && (
            <h3 className="pb-2 text-sm font-semibold text-muted">
              Attempt {attempt}
            </h3>
          )}
          <ul className="flex flex-col gap-2">
            {results
              .filter((r) => r.attempt_number === attempt)
              .map((r) => (
                <ValidationRow key={r.id} result={r} />
              ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

function ValidationRow({ result }: { result: ValidationResult }) {
  return (
    <li className="rounded border border-border bg-surface px-3 py-2">
      <div className="flex items-baseline gap-3">
        <span
          className={`text-sm font-semibold ${STATUS_CLASSES[result.status] ?? "text-muted"}`}
        >
          {result.status.replaceAll("_", " ")}
        </span>
        <span className="text-base font-semibold text-foreground">
          {result.name}
        </span>
        <span className="text-sm text-muted">
          {result.category.replaceAll("_", " ")}
        </span>
        <TrustBadge trusted={result.trusted_execution} />
        <span className="ml-auto flex items-baseline gap-3 text-sm text-muted">
          {result.exit_code !== null && (
            <span className="font-mono">exit {result.exit_code}</span>
          )}
          <span>{formatDuration(result.duration_ms)}</span>
        </span>
      </div>
      {Array.isArray(result.command) && result.command.length > 0 && (
        <p className="mt-1 font-mono text-sm break-all text-muted">
          $ {result.command.join(" ")}
        </p>
      )}
      {result.summary && (
        <p className="mt-1 max-w-[72ch] text-sm text-muted">{result.summary}</p>
      )}
    </li>
  );
}
