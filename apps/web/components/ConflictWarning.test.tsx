import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { ConflictWarning } from "./ConflictWarning";
import type { TaskConflict } from "@/lib/types";

afterEach(cleanup);

const conflict: TaskConflict = {
  id: "c-1",
  other_task_id: "3b241101-e2bb-4255-8caf-4136c566a962",
  other_task_title: "Upgrade the TLS library",
  kinds: ["file_overlap", "merge_conflict"],
  files: ["go.mod", "internal/tls/dial.go"],
  detected_at: "2026-07-30T12:00:00Z",
  updated_at: "2026-07-30T12:00:00Z",
};

test("renders nothing without conflicts", () => {
  const { container } = render(<ConflictWarning conflicts={[]} />);
  expect(container.innerHTML).toBe("");
});

test("lists each conflicting task with kinds and files", () => {
  render(<ConflictWarning conflicts={[conflict]} />);

  expect(screen.getByText("Overlaps an active task")).toBeDefined();
  const link = screen.getByRole("link", { name: "Upgrade the TLS library" });
  expect(link.getAttribute("href")).toBe(
    "/tasks/3b241101-e2bb-4255-8caf-4136c566a962",
  );
  expect(screen.getByText("same files, merge conflict")).toBeDefined();
  expect(screen.getByText(/go\.mod/)).toBeDefined();
});

test("pluralizes for several conflicts", () => {
  render(
    <ConflictWarning
      conflicts={[
        conflict,
        {
          ...conflict,
          id: "c-2",
          other_task_id: "t-3",
          other_task_title: "Other",
        },
      ]}
    />,
  );
  expect(screen.getByText("Overlaps active tasks")).toBeDefined();
});
