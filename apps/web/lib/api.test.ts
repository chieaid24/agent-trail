import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ApiError,
  cancelTask,
  eventCursor,
  getEvidence,
  getRepository,
  getRunner,
  listOrganizations,
  listRepositories,
  listRunners,
  listTasks,
  streamUrl,
} from "./api";
import type { ActivityEvent } from "./types";

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("api client", () => {
  it("unwraps the tasks envelope and passes filters", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse(200, { tasks: [{ id: "t1" }] }));
    vi.stubGlobal("fetch", fetchMock);

    const tasks = await listTasks({ status: "queued", limit: 10 });
    expect(tasks).toEqual([{ id: "t1" }]);
    expect(fetchMock).toHaveBeenCalledWith(
      "/backend/api/v1/tasks?status=queued&limit=10",
      undefined,
    );
  });

  it("surfaces the server error envelope as ApiError", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(jsonResponse(409, { error: "invalid transition" })),
    );
    await expect(cancelTask("t1")).rejects.toMatchObject({
      name: "ApiError",
      status: 409,
      message: "invalid transition",
    });
  });

  it("maps a network failure to status 0", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("offline")));
    await expect(listTasks()).rejects.toMatchObject({ status: 0 });
  });

  it("unwraps dashboard collections", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        jsonResponse(200, { organizations: [{ id: "o1" }] }),
      )
      .mockResolvedValueOnce(
        jsonResponse(200, { repositories: [{ id: "r1" }] }),
      )
      .mockResolvedValueOnce(jsonResponse(200, { runners: [{ id: "w1" }] }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(listOrganizations()).resolves.toEqual([{ id: "o1" }]);
    await expect(
      listRepositories({ organizationId: "o1", limit: 5 }),
    ).resolves.toEqual([{ id: "r1" }]);
    await expect(listRunners()).resolves.toEqual([{ id: "w1" }]);
    expect(fetchMock.mock.calls.map((call) => call[0])).toEqual([
      "/backend/api/v1/organizations",
      "/backend/api/v1/organizations/o1/repositories?limit=5",
      "/backend/api/v1/runners",
    ]);
  });

  it("loads repository and runner detail", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(200, { id: "r1" }))
      .mockResolvedValueOnce(jsonResponse(200, { id: "w1" }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(getRepository("r1")).resolves.toMatchObject({ id: "r1" });
    await expect(getRunner("w1")).resolves.toMatchObject({ id: "w1" });
  });

  it("treats a missing evidence report as null, not an error", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          jsonResponse(404, { error: "no evidence report for task" }),
        ),
    );
    await expect(getEvidence("t1")).resolves.toBeNull();
  });

  it("rethrows non-404 evidence failures", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse(500, { error: "internal error" })),
    );
    await expect(getEvidence("t1")).rejects.toBeInstanceOf(ApiError);
  });
});

describe("stream helpers", () => {
  it("builds the resume cursor from attempt and sequence", () => {
    const e = { attempt_number: 2, sequence_number: 17 } as ActivityEvent;
    expect(eventCursor(e)).toBe("2:17");
  });

  it("builds the stream url with and without a cursor", () => {
    expect(streamUrl("t1")).toBe("/backend/api/v1/tasks/t1/stream");
    expect(streamUrl("t1", "2:17")).toBe(
      "/backend/api/v1/tasks/t1/stream?last_event_id=2%3A17",
    );
  });
});
