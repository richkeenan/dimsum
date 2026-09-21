import { afterEach, describe, expect, it, vi } from "vitest";
import { api, APIError, count, percentage, normalizeSettings } from "./api";
afterEach(() => vi.unstubAllGlobals());
describe("API boundary", () => {
  it("uses the documented nested saved revision, including invalid disk status", () => {
    expect(
      normalizeSettings({
        status: {
          saved_revision: "invalid-disk",
          active_generation: "9",
          pending: false,
        },
        configuration_error: "line 8 invalid",
      }),
    ).toMatchObject({
      revision: "invalid-disk",
      active_generation: "9",
      error: "line 8 invalid",
    });
  });
  it("sends the login CSRF token on later mutations and clears it on logout", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(
          '{"csrf_token":"csrf-test","expires_at":"2026-09-22T12:00:00Z"}',
        ),
      )
      .mockImplementation(() => Promise.resolve(new Response("{}")));
    vi.stubGlobal("fetch", fetcher);
    await api.login("password");
    await api.edit("rev", [{ path: ["cache", "bytes"], value: 2048 }]);
    expect(fetcher.mock.calls[1][1].headers["X-CSRF-Token"]).toBe("csrf-test");
    await api.logout();
    expect(sessionStorage.getItem("dimsum-csrf")).toBeNull();
  });
  it("preserves decimal counters beyond Number precision", () => {
    expect(count("9007199254740993")).toBe("9,007,199,254,740,993");
    expect(percentage("1", "4")).toBe("25.0%");
    expect(percentage(undefined, "4")).toBe("—");
  });
  it("sends surgical edits with the exact read revision", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValue(new Response('{"revision":"next"}'));
    vi.stubGlobal("fetch", fetcher);
    await api.edit("disk-revision", [
      { path: ["cache", "bytes"], value: 1024 },
    ]);
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({
      revision: "disk-revision",
      edits: [{ path: ["cache", "bytes"], value: 1024 }],
    });
  });
  it("keeps conflict field errors and announces auth expiry", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(
          new Response(
            '{"code":"revision_conflict","message":"Changed","fields":{"revision":"stale"}}',
            { status: 409 },
          ),
        )
        .mockResolvedValueOnce(new Response("{}", { status: 401 })),
    );
    await expect(api.get("settings")).rejects.toMatchObject({
      status: 409,
      fields: { revision: "stale" },
    });
    const expired = vi.fn();
    window.addEventListener("session-expired", expired, { once: true });
    await expect(api.get("summary")).rejects.toBeInstanceOf(APIError);
    expect(expired).toHaveBeenCalledOnce();
  });
});
