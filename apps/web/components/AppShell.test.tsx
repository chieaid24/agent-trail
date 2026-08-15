import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { AppShell } from "./AppShell";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

test("shows the session user and sign-out once /me answers", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          user: {
            github_login: "octocat",
            avatar_url: "https://avatars.test/42",
          },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    ),
  );
  render(<AppShell>content</AppShell>);

  expect(await screen.findByText("octocat")).toBeDefined();
  expect(screen.getByRole("button", { name: "Sign out" })).toBeDefined();
  expect(screen.queryByText("local control plane")).toBeNull();
});

test("keeps the plain footer when auth is not configured", async () => {
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValue(
        new Response(
          JSON.stringify({ error: "authentication is not configured" }),
          { status: 503, headers: { "Content-Type": "application/json" } },
        ),
      ),
  );
  render(<AppShell>content</AppShell>);

  expect(await screen.findByText("local control plane")).toBeDefined();
  expect(screen.queryByRole("button", { name: "Sign out" })).toBeNull();
});

test("links the installations page in the primary navigation", () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(new Response("{}", { status: 200 })),
  );
  render(<AppShell>content</AppShell>);

  const link = screen.getByRole("link", { name: "Installations" });
  expect(link.getAttribute("href")).toBe("/installations");
});
