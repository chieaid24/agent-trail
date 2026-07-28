// The trust distinction (DESIGN.md, VISION.md principle 4): a filled badge
// marks a platform-verified fact, an outlined badge marks an agent claim.
// Shape carries the meaning, not hue, so the two never blur.
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
