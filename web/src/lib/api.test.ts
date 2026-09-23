import { afterEach, describe, expect, it, vi } from "vitest";
import {
  api,
  APIError,
  count,
  percentage,
  normalizeSettings,
  queryParameters,
  historyWindow,
  microsecondsToMS,
  backupURL,
  archiveBase64,
  maxArchiveBytes,
} from "./api";
afterEach(() => vi.unstubAllGlobals());
describe("API boundary", () => {
  it("sends only supported nonempty provider filters and opaque cursors", () => {
    expect(
      queryParameters(
        {
          name: "",
          client: "192.0.2.1",
          outcome: "cache",
          upstream: "9",
          source: "x",
        },
        "",
      ).toString(),
    ).toBe("limit=100&client=192.0.2.1&outcome=cache");
    expect(queryParameters({}, "9007199254740993").get("cursor")).toBe("9007199254740993");
  });
  it("keeps exact range boundaries and requests a bounded number of provider buckets", () => {
    for (const preset of ["1h", "24h", "7d"]) {
      const now = Date.parse("2026-09-21T12:34:56Z");
      const w = historyWindow(preset, now);
      const p = new URLSearchParams(w.params);
      const from = Date.parse(p.get("from")!),
        to = Date.parse(p.get("to")!);
      expect(to).toBe(now);
      expect(to - from).toBe(
        ({ "1h": 3600000, "24h": 86400000, "7d": 604800000 } as Record<string, number>)[preset],
      );
      expect(
        Math.ceil(to / (w.resolution * 1000)) - Math.floor(from / (w.resolution * 1000)),
      ).toBeLessThanOrEqual(1500);
    }
    const custom = { from: "2026-01-01T12:34:56Z", to: "2026-09-21T12:34:56Z" };
    const result = historyWindow("custom", 0, custom);
    const p = new URLSearchParams(result.params);
    expect(Date.parse(p.get("from")!)).toBe(Date.parse(custom.from));
    expect(Date.parse(p.get("to")!)).toBe(Date.parse(custom.to));
    expect(result.resolution).toBe(86400);
  });
  it("formats duration decimals exactly and restricts download links to backup artifacts", () => {
    expect(microsecondsToMS("9007199254740993")).toBe("9,007,199,254,740.993");
    expect(microsecondsToMS("180")).toBe("0.180");
    expect(backupURL({ download_url: "https://evil.test" })).toBeUndefined();
    expect(backupURL({ download_url: "/api/v1/config/backups/" + "a".repeat(32) })).toBe(
      "/api/v1/config/backups/" + "a".repeat(32),
    );
  });
  it("rejects oversized and empty archives before reading or encoding them", async () => {
    const arrayBuffer = vi.fn();
    await expect(
      archiveBase64({
        size: maxArchiveBytes + 1,
        arrayBuffer,
      } as unknown as File),
    ).rejects.toThrow("2 MiB");
    await expect(archiveBase64({ size: 0, arrayBuffer } as unknown as File)).rejects.toThrow(
      "empty",
    );
    expect(arrayBuffer).not.toHaveBeenCalled();
    const bytes = new Uint8Array([0, 255, 128, 42]);
    expect(
      await archiveBase64({
        size: 4,
        arrayBuffer: async () => bytes.buffer,
      } as File),
    ).toBe("AP+AKg==");
  });
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
        new Response('{"csrf_token":"csrf-test","expires_at":"2026-09-22T12:00:00Z"}'),
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
  it("localizes counts, percentages and exact durations without losing precision", () => {
    expect(count("9007199254740993", "de-DE")).toBe("9.007.199.254.740.993");
    expect(percentage("1", "4", "de-DE")).toBe("25,0\u00a0%");
    expect(percentage("1", "0", "de-DE")).toBe("—");
    expect(microsecondsToMS("9007199254740993", "de-DE")).toBe("9.007.199.254.740,993");
    expect(microsecondsToMS("1001", "de-DE")).toBe("1,001");
    expect(microsecondsToMS("1001", "ar-EG")).toBe("١٫٠٠١");
    expect(microsecondsToMS(undefined, "de-DE")).toBe("—");
  });
  it("sends surgical edits with the exact read revision", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response('{"revision":"next"}'));
    vi.stubGlobal("fetch", fetcher);
    await api.edit("disk-revision", [{ path: ["cache", "bytes"], value: 1024 }]);
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
