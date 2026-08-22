export function TrustBadge({ trusted }: { trusted: boolean }) {
  if (trusted) {
    return (
      <span className="inline-flex items-center rounded bg-border px-1.5 py-0.5 text-sm font-semibold whitespace-nowrap text-foreground">
        verified
      </span>
    );
  }
  return (
    <span className="inline-flex items-center rounded border border-border px-1.5 py-0.5 text-sm whitespace-nowrap text-muted">
      agent claim
    </span>
  );
}
