declare function expand(
  pattern: string,
  options?: { max?: number; maxLength?: number },
): string[];

declare namespace expand {
  const expand: typeof import("./index.d.cts");
  const EXPANSION_MAX: number;
  const EXPANSION_MAX_LENGTH: number;
}

export = expand;
