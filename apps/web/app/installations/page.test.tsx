import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import Installations from "./page";
import type { Organization, Repository } from "@/lib/types";

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

const org = {
  id: "6c4f8ea8-2131-436d-8fd8-8acaf079c2a2",
  github_account_login: "acme",
  github_account_type: "Organization",
  repository_count: 1,
  enabled_repository_count: 1,
} as Organization;

const repo = {
  id: "39be2f56-3419-4a0a-a7ad-1a72698c0cc5",
  organization_id: org.id,
  full_name: "acme/widget",
  is_private: true,
  is_enabled: true,
} as Repository;

function installationsFetch(options?: {
  organizations?: Organization[];
  repositories?: Repository[];
}) {
  const organizations = options?.organizations ?? [org];
  const repositories = options?.repositories ?? [repo];
  return vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (url === "/backend/me") {
      return jsonResponse({
        user: { github_login: "octocat" },
        install_url: "https://github.test/apps/agent-trail/installations/new",
      });
    }
    if (url.includes("/disable") || url.includes("/enable")) {
      expect(init?.method).toBe("POST");
      return jsonResponse({ ...repo, is_enabled: url.includes("/enable") });
    }
    if (url.includes("/repositories")) return jsonResponse({ repositories });
    return jsonResponse({ organizations });
  });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

test("lists accounts with their repositories and enablement", async () => {
  vi.stubGlobal("fetch", installationsFetch());
  render(<Installations />);

  expect(await screen.findByText("acme")).toBeDefined();
  expect(screen.getByText("acme/widget")).toBeDefined();
  expect(screen.getByText("private")).toBeDefined();
  expect(screen.getByText("1 of 1 enabled")).toBeDefined();
  expect(screen.getByText("Enabled: agents may run here")).toBeDefined();
  const install = screen.getByRole("link", {
    name: "Install on another account",
  });
  expect(install.getAttribute("href")).toContain("/installations/new");
});

test("disables a repository from its row", async () => {
  vi.stubGlobal("fetch", installationsFetch());
  render(<Installations />);

  const toggle = await screen.findByRole("button", { name: "Disable" });
  fireEvent.click(toggle);

  expect(
    await screen.findByText("Disabled: agents will not run here"),
  ).toBeDefined();
  expect(screen.getByRole("button", { name: "Enable" })).toBeDefined();
  expect(screen.getByText("0 of 1 enabled")).toBeDefined();
});

test("shows the install action when no account is connected", async () => {
  vi.stubGlobal(
    "fetch",
    installationsFetch({ organizations: [], repositories: [] }),
  );
  render(<Installations />);

  expect(
    await screen.findByText("No GitHub account is connected yet."),
  ).toBeDefined();
  const link = screen.getByRole("link", { name: "Install the GitHub App" });
  expect(link.getAttribute("href")).toContain("/installations/new");
});
