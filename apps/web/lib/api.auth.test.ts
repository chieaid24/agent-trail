import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, getMe, logout, setRepositoryEnabled } from "./api";

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("auth api client", () => {
  it("fetches the session user from the unversioned /me", async () => {
    const me = { user: { github_login: "octocat" } };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, me));
    vi.stubGlobal("fetch", fetchMock);

    expect(await getMe()).toEqual(me);
    expect(fetchMock).toHaveBeenCalledWith("/backend/me", undefined);
  });

  it("posts the logout and tolerates the empty 204 body", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);

    await logout();
    expect(fetchMock).toHaveBeenCalledWith("/backend/auth/logout", {
      method: "POST",
    });
  });

  it("posts enable and disable to the repository action routes", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse(200, { id: "r1", is_enabled: true }));
    vi.stubGlobal("fetch", fetchMock);

    await setRepositoryEnabled("r1", true);
    expect(fetchMock).toHaveBeenCalledWith(
      "/backend/api/v1/repositories/r1/enable",
      { method: "POST" },
    );

    await setRepositoryEnabled("r1", false);
    expect(fetchMock).toHaveBeenCalledWith(
      "/backend/api/v1/repositories/r1/disable",
      { method: "POST" },
    );
  });

  it("surfaces a 401 as an ApiError", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          jsonResponse(401, { error: "authentication required" }),
        ),
    );

    const err = await getMe().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(401);
  });
});
