import { expect, test } from "vitest";
import { GET } from "./route";

test("GET /healthz returns 200 without rendering a page", async () => {
  const response = GET();
  expect(response.status).toBe(200);
  expect(response.headers.get("Content-Type")).toBe("text/plain");
  expect(response.headers.get("Cache-Control")).toBe("no-store");
  await expect(response.text()).resolves.toBe("ok");
});
