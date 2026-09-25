import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { StatusBadge } from "./StatusBadge";

afterEach(cleanup);

test("revision requested has its own tone, distinct from awaiting review", () => {
  render(<StatusBadge status="revision_requested" />);
  const badge = screen.getByText("revision requested");
  expect(badge.className).toContain("text-info");
  expect(badge.querySelector("span")?.className).toContain("bg-info");
  cleanup();
  render(<StatusBadge status="awaiting_review" />);
  expect(screen.getByText("awaiting review").className).toContain(
    "text-warning",
  );
});

test("only running statuses breathe", () => {
  render(<StatusBadge status="executing" />);
  expect(
    screen.getByText("executing").querySelector("span")?.className,
  ).toContain("dot-breathe");
  cleanup();
  render(<StatusBadge status="revision_requested" />);
  expect(
    screen.getByText("revision requested").querySelector("span")?.className,
  ).not.toContain("dot-breathe");
});
