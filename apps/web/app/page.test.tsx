import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import Home from "./page";

test("renders the app shell and empty state", () => {
  render(<Home />);

  expect(screen.getByText("Agent Trail")).toBeDefined();
  expect(screen.getByRole("navigation", { name: "Primary" })).toBeDefined();
  expect(screen.getByText("/agent-trail run")).toBeDefined();

  const docs = screen.getByRole("link", { name: "Read the docs" });
  expect(docs.getAttribute("href")).toContain("github.com");
});
