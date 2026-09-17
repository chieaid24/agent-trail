import { afterEach, expect, test, vi } from "vitest";
import { apiProxyUrl } from "./apiProxy";

afterEach(() => {
  vi.unstubAllEnvs();
});

test("rewrites /backend/* to API_PROXY_TARGET, keeping path and query", () => {
  vi.stubEnv("API_PROXY_TARGET", "http://control-plane:8080/");
  expect(
    apiProxyUrl(new URL("http://dashboard.local/backend/api/v1/tasks?limit=5"))
      .href,
  ).toBe("http://control-plane:8080/api/v1/tasks?limit=5");
});

test("reads API_PROXY_TARGET per call, not once at load", () => {
  const url = new URL("http://dashboard.local/backend/me");
  vi.stubEnv("API_PROXY_TARGET", "http://first:8080");
  expect(apiProxyUrl(url).origin).toBe("http://first:8080");
  vi.stubEnv("API_PROXY_TARGET", "http://second:8080");
  expect(apiProxyUrl(url).origin).toBe("http://second:8080");
});

test("defaults to the local API when API_PROXY_TARGET is unset", () => {
  vi.stubEnv("API_PROXY_TARGET", undefined);
  expect(
    apiProxyUrl(new URL("http://dashboard.local/backend/healthz")).href,
  ).toBe("http://localhost:8080/healthz");
});
