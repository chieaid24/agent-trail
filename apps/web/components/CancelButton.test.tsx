import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { CancelButton } from "./CancelButton";
import type { Task } from "@/lib/types";

const task = { id: "3b241101-e2bb-4255-8caf-4136c566a962" } as Task;

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

test("arms inline confirmation and backs out without cancelling", () => {
  const fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
  render(<CancelButton task={task} onCancelled={() => {}} />);

  fireEvent.click(screen.getByRole("button", { name: "Cancel task" }));
  expect(screen.getByText("Cancel this task?")).toBeDefined();

  fireEvent.click(screen.getByRole("button", { name: "Keep running" }));
  expect(screen.getByRole("button", { name: "Cancel task" })).toBeDefined();
  expect(fetchMock).not.toHaveBeenCalled();
});

test("confirming sends the reason and reports the cancelled task", async () => {
  const cancelled = { ...task, status: "cancelled" };
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValue(
        new Response(JSON.stringify(cancelled), { status: 200 }),
      ),
  );
  const onCancelled = vi.fn();
  render(<CancelButton task={task} onCancelled={onCancelled} />);

  fireEvent.click(screen.getByRole("button", { name: "Cancel task" }));
  fireEvent.change(screen.getByLabelText("Cancellation reason"), {
    target: { value: "wrong branch" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Confirm cancel" }));

  expect(
    await screen.findByRole("button", { name: "Cancel task" }),
  ).toBeDefined();
  expect(onCancelled).toHaveBeenCalledWith(cancelled);
});

test("a rejected cancel surfaces the server error inline", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: "invalid transition" }), {
        status: 409,
      }),
    ),
  );
  render(<CancelButton task={task} onCancelled={() => {}} />);

  fireEvent.click(screen.getByRole("button", { name: "Cancel task" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm cancel" }));

  expect(
    await screen.findByText("Cancel failed: invalid transition."),
  ).toBeDefined();
  // The idle control returns so the user can retry.
  expect(screen.getByRole("button", { name: "Cancel task" })).toBeDefined();
});
