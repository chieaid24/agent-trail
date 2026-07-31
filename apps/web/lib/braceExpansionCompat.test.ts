import { createRequire } from "node:module";
import { describe, expect, it } from "vitest";

type Expand = {
  (pattern: string): string[];
  expand: (pattern: string) => string[];
  EXPANSION_MAX: number;
  EXPANSION_MAX_LENGTH: number;
};

const expand = createRequire(import.meta.url)("brace-expansion") as Expand;

describe("brace-expansion compatibility", () => {
  it("preserves the callable CommonJS API", () => {
    expect(expand("file-{a,b}.ts")).toEqual(["file-a.ts", "file-b.ts"]);
    expect(expand.expand("file-{a,b}.ts")).toEqual(["file-a.ts", "file-b.ts"]);
  });

  it("exposes the upstream expansion limits", () => {
    expect(expand.EXPANSION_MAX).toBe(100_000);
    expect(expand.EXPANSION_MAX_LENGTH).toBe(4_000_000);
  });
});
