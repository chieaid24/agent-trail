import { describe, expect, it } from "vitest";
import {
  formatDuration,
  outcomeReason,
  runtimeMs,
  shortSha,
  statusLabel,
} from "./format";

describe("formatDuration", () => {
  it("scales units with duration", () => {
    expect(formatDuration(180)).toBe("180ms");
    expect(formatDuration(12_000)).toBe("12s");
    expect(formatDuration(204_000)).toBe("3m 24s");
    expect(formatDuration(4_080_000)).toBe("1h 08m");
  });
});

describe("runtimeMs", () => {
  it("is null before the task starts", () => {
    expect(runtimeMs(null, null)).toBeNull();
  });

  it("measures completed runs from the recorded bounds", () => {
    expect(runtimeMs("2026-07-28T12:00:00Z", "2026-07-28T12:03:24Z")).toBe(
      204_000,
    );
  });
});

describe("labels", () => {
  it("humanizes statuses and truncates shas", () => {
    expect(statusLabel("awaiting_review")).toBe("awaiting review");
    expect(shortSha("0123456789abcdef0123")).toBe("0123456789");
  });
});

describe("outcomeReason", () => {
  it("returns the reason only for completed and cancelled tasks", () => {
    expect(
      outcomeReason({
        status: "completed",
        status_reason: "pull request #7 merged",
      }),
    ).toBe("pull request #7 merged");
    expect(
      outcomeReason({ status: "cancelled", status_reason: "duplicate" }),
    ).toBe("duplicate");
    expect(
      outcomeReason({ status: "completed", status_reason: null }),
    ).toBeNull();
    expect(
      outcomeReason({ status: "queued", status_reason: "revision requested" }),
    ).toBeNull();
    expect(
      outcomeReason({ status: "failed", status_reason: "validation failed" }),
    ).toBeNull();
  });
});
