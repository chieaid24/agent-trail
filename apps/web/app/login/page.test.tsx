import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import LoginPage from "./page";

const searchParams = new URLSearchParams();

vi.mock("next/navigation", () => ({
  useSearchParams: () => searchParams,
}));

afterEach(() => {
  cleanup();
  searchParams.delete("error");
});

test("renders the sign-in action", () => {
  render(<LoginPage />);

  expect(screen.getByText("Agent Trail")).toBeDefined();
  const link = screen.getByRole("link", { name: /Sign in with GitHub/ });
  expect(link.getAttribute("href")).toBe("/backend/auth/github/start");
  expect(screen.queryByRole("alert")).toBeNull();
});

test("explains a failed callback", () => {
  searchParams.set("error", "github_denied");
  render(<LoginPage />);

  expect(screen.getByRole("alert").textContent).toBe(
    "GitHub denied the authorization request.",
  );
});

test("falls back to a generic message for unknown error codes", () => {
  searchParams.set("error", "mystery");
  render(<LoginPage />);

  expect(screen.getByRole("alert").textContent).toBe(
    "Signing in failed. Try again.",
  );
});
