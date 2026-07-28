import { TrustBadge } from "./TrustBadge";
import { formatDateTime, formatDuration, shortSha } from "@/lib/format";
import type { StoredEvidence } from "@/lib/types";

// The evidence report, rendered from the structured document rather than
// its markdown mirror: the same facts, natively typed. Prose surfaces are
// measure-limited (DESIGN.md).
export function EvidencePanel({
  evidence,
}: {
  evidence: StoredEvidence | null;
}) {
  if (evidence === null) {
    return (
      <p className="py-8 text-sm text-muted">
        No evidence report yet. It is generated after validation completes.
      </p>
    );
  }

  const r = evidence.report;
  const trusted = r.validation.filter((v) => v.trusted_execution);
  const claimed = r.validation.filter((v) => !v.trusted_execution);

  const provenance: [string, string, boolean][] = [];
  if (r.execution.base_commit)
    provenance.push(["base commit", shortSha(r.execution.base_commit), true]);
  if (r.execution.final_commit)
    provenance.push(["final commit", shortSha(r.execution.final_commit), true]);
  if (r.execution.agent_provider)
    provenance.push(["provider", r.execution.agent_provider, false]);
  if (r.execution.agent_model)
    provenance.push(["model", r.execution.agent_model, false]);
  if (r.execution.duration_seconds !== undefined)
    provenance.push([
      "duration",
      formatDuration(r.execution.duration_seconds * 1000),
      false,
    ]);
  provenance.push(["schema", `v${r.schema_version}`, false]);
  provenance.push(["generated", formatDateTime(evidence.created_at), false]);

  return (
    <div className="mt-2 max-w-[72ch]">
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
        {provenance.map(([label, value, mono]) => (
          <Provenance key={label} label={label} value={value} mono={mono} />
        ))}
      </dl>

      <section className="mt-8">
        <h3 className="flex items-center gap-2 text-base font-semibold">
          Verified by Agent Trail <TrustBadge trusted />
        </h3>
        {trusted.length === 0 ? (
          <p className="mt-2 text-sm text-muted">No trusted checks ran.</p>
        ) : (
          <ChecksTable
            checks={trusted}
            caption="Checks the platform executed in the workspace after editing ended."
          />
        )}
      </section>

      {claimed.length > 0 && (
        <section className="mt-8">
          <h3 className="flex items-center gap-2 text-base font-semibold">
            Agent-reported <TrustBadge trusted={false} />
          </h3>
          <ChecksTable
            checks={claimed}
            caption="Claims the agent made; not independently verified."
          />
        </section>
      )}

      {r.plan && r.plan.length > 0 && (
        <section className="mt-8">
          <h3 className="text-base font-semibold">Plan</h3>
          <ol className="mt-2 list-decimal pl-5 text-sm text-foreground">
            {r.plan.map((step, i) => (
              <li key={i} className="py-0.5">
                {step}
              </li>
            ))}
          </ol>
        </section>
      )}

      <section className="mt-8">
        <h3 className="text-base font-semibold">Changes</h3>
        {r.changes.files && r.changes.files.length > 0 ? (
          <ul className="mt-2 font-mono text-sm text-foreground">
            {r.changes.files.map((f) => (
              <li key={f} className="py-0.5 break-all">
                {f}
              </li>
            ))}
          </ul>
        ) : (
          <p className="mt-2 text-sm text-muted">
            {r.changes.files_changed} file
            {r.changes.files_changed === 1 ? "" : "s"} changed.
          </p>
        )}
      </section>

      {r.risks && r.risks.length > 0 && (
        <ProseList title="Risks" items={r.risks} />
      )}
      {r.unverified && r.unverified.length > 0 && (
        <ProseList title="Unverified" items={r.unverified} />
      )}
    </div>
  );
}

function Provenance({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono: boolean;
}) {
  return (
    <>
      <dt className="text-sm text-muted">{label}</dt>
      <dd className={`text-sm text-foreground ${mono ? "font-mono" : ""}`}>
        {value}
      </dd>
    </>
  );
}

function ChecksTable({
  checks,
  caption,
}: {
  checks: StoredEvidence["report"]["validation"];
  caption: string;
}) {
  return (
    <div className="mt-2">
      <p className="text-sm text-muted">{caption}</p>
      <table className="mt-2 w-full text-sm">
        <thead>
          <tr className="border-b border-border text-left text-muted">
            <th className="py-1 pr-4 font-normal">Check</th>
            <th className="py-1 pr-4 font-normal">Category</th>
            <th className="py-1 pr-4 font-normal">Result</th>
            <th className="py-1 pr-4 text-right font-normal">Exit</th>
            <th className="py-1 text-right font-normal">Duration</th>
          </tr>
        </thead>
        <tbody>
          {checks.map((c, i) => (
            <tr key={`${c.name}-${i}`} className="border-b border-border">
              <td className="py-1 pr-4 font-semibold text-foreground">
                {c.name}
              </td>
              <td className="py-1 pr-4 text-muted">
                {(c.category ?? "").replaceAll("_", " ")}
              </td>
              <td
                className={`py-1 pr-4 font-semibold ${
                  c.status === "passed"
                    ? "text-success"
                    : c.status === "failed"
                      ? "text-danger"
                      : "text-warning"
                }`}
              >
                {c.status.replaceAll("_", " ")}
              </td>
              <td className="py-1 pr-4 text-right font-mono text-muted">
                {c.exit_code ?? ""}
              </td>
              <td className="py-1 text-right text-muted">
                {c.duration_ms !== undefined
                  ? formatDuration(c.duration_ms)
                  : ""}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ProseList({ title, items }: { title: string; items: string[] }) {
  return (
    <section className="mt-8">
      <h3 className="text-base font-semibold">{title}</h3>
      <ul className="mt-2 list-disc pl-5 text-sm text-foreground">
        {items.map((item, i) => (
          <li key={i} className="py-0.5">
            {item}
          </li>
        ))}
      </ul>
    </section>
  );
}
