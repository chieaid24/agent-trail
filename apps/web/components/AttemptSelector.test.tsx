import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { AttemptSelector } from "./AttemptSelector";
import type { TaskAttempt } from "@/lib/types";

afterEach(cleanup);

function attempt(overrides: Partial<TaskAttempt>): TaskAttempt {
  return {
    id: "3b241101-e2bb-4255-8caf-4136c566a962",
    task_id: "3b241101-e2bb-4255-8caf-4136c566a961",
    attempt_number: 2,
    status: "active",
    base_commit_sha: "c".repeat(40),
    final_commit_sha: null,
    pull_request_number: 7,
    instructions: "revise",
    requested_by_login: "alice",
    trigger_comment_id: 9,
    trigger_check_run_id: null,
    trigger_check_run_completed_at: null,
    feedback: [
      {
        kind: "review_comment",
        author: "alice",
        posted_at: "2026-09-24T12:00:00Z",
      },
      {
        kind: "revise_command",
        author: "alice",
        posted_at: "2026-09-24T12:01:00Z",
      },
    ],
    failure_code: null,
    failure_message: null,
    started_at: null,
    completed_at: null,
    created_at: "2026-09-24T12:01:00Z",
    ...overrides,
  };
}

test("renders nothing for a single attempt", () => {
  const { container } = render(
    <AttemptSelector numbers={[1]} selected={1} onSelect={() => {}} />,
  );
  expect(container.innerHTML).toBe("");
});

test("marks the selected attempt and reports the requester", () => {
  const onSelect = vi.fn();
  render(
    <AttemptSelector
      numbers={[1, 2, 3]}
      selected={3}
      attempt={attempt({ attempt_number: 3 })}
      onSelect={onSelect}
    />,
  );
  const group = screen.getByRole("group", { name: "Attempt selector" });
  expect(group.textContent).toContain("3 attempts");
  expect(
    screen
      .getByRole("button", { name: "Attempt 3" })
      .getAttribute("aria-pressed"),
  ).toBe("true");
  expect(
    screen
      .getByRole("button", { name: "Attempt 1" })
      .getAttribute("aria-pressed"),
  ).toBe("false");
  expect(screen.getByLabelText("Attempt 3 request").textContent).toContain(
    "Revision requested by alice",
  );
  expect(screen.getByLabelText("Attempt 3 request").textContent).toContain(
    "with 2 feedback items",
  );
  fireEvent.click(screen.getByRole("button", { name: "Attempt 1" }));
  expect(onSelect).toHaveBeenCalledWith(1);
});

test("describes attempt 1 as the run and tolerates a missing requester", () => {
  render(
    <AttemptSelector
      numbers={[1, 2]}
      selected={1}
      attempt={attempt({
        attempt_number: 1,
        requested_by_login: null,
        feedback: [],
      })}
      onSelect={() => {}}
    />,
  );
  const line = screen.getByLabelText("Attempt 1 request").textContent ?? "";
  expect(line).toContain("Run requested");
  expect(line).not.toContain(" by ");
  expect(line).not.toContain("feedback");
});
