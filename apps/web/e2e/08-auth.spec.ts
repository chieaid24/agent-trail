import { expect, test } from "@playwright/test";
import { shoot, shootBothViewports } from "./harness/shots";

// Signed-out flows: a fresh context with no session cookie.
test.describe("signed out", () => {
  test.use({ storageState: { cookies: [], origins: [] } });

  test("the dashboard redirects to the login page", async ({ page }) => {
    await page.goto("/");
    await expect(page).toHaveURL(/\/login$/);
    await expect(
      page.getByRole("link", { name: "Sign in with GitHub" }),
    ).toBeVisible();
    await shootBothViewports(page, "login");
  });

  test("a failed callback explains itself on the login page", async ({
    page,
  }) => {
    await page.goto("/login?error=github_denied");
    await expect(page.getByRole("alert")).toHaveText(
      "GitHub denied the authorization request.",
    );
    await shoot(page, "login-error");
  });

  test("signing in with github lands on the dashboard", async ({ page }) => {
    // The full browser round-trip: start redirects through the /backend
    // proxy to the fake GitHub authorize URL, back into the callback on
    // this origin, and the session cookie lands first-party.
    await page.goto("/login");
    await page.getByRole("link", { name: "Sign in with GitHub" }).click();
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByText("e2e-octocat")).toBeVisible();
  });
});

// Signed-in flows run on the shared harness session (storageState).
test("installations lists the account and toggles repository enablement", async ({
  page,
}) => {
  await page.goto("/installations");

  // The seeded organization, matched to the fake /user/installations.
  const section = page.getByRole("region", { name: "chieaid24" });
  await expect(section).toBeVisible();
  const row = section
    .locator("li")
    .filter({ hasText: "chieaid24/runner-images" })
    .first();
  await expect(row.getByText("Enabled: agents may run here")).toBeVisible();
  await shootBothViewports(page, "installations");

  await row.getByRole("button", { name: "Disable" }).click();
  await expect(
    row.getByText("Disabled: agents will not run here"),
  ).toBeVisible();
  await shoot(page, "installations-disabled");

  // Leave the seeded state as it was for any spec after this one.
  await row.getByRole("button", { name: "Enable" }).click();
  await expect(row.getByText("Enabled: agents may run here")).toBeVisible();
});

test("the shell shows the session user and signs out", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByText("e2e-octocat")).toBeVisible();
  await shoot(page, "shell-session-user");

  // Signing out revokes the shared harness session, so this spec runs
  // last: everything after it would be signed out too.
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login$/);

  // The revoked session is gone server-side: going back to the dashboard
  // bounces straight back to the login page.
  await page.goto("/");
  await expect(page).toHaveURL(/\/login$/);
});
