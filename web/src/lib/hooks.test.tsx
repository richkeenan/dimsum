import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useLive, useResource } from "./hooks";
afterEach(() => vi.unstubAllGlobals());
it("aborts superseded query pages and never displays their late results", async () => {
  const responses: Array<{
    resolve: (r: Response) => void;
    signal: AbortSignal;
  }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(
      (_url: string, options: RequestInit) =>
        new Promise<Response>((resolve) =>
          responses.push({ resolve, signal: options.signal as AbortSignal }),
        ),
    ),
  );
  const { result, rerender, unmount } = renderHook(
    ({ path }) => useResource<{ items: string[] }>(path),
    { initialProps: { path: "queries?name=first" } },
  );
  rerender({ path: "queries?name=second" });
  expect(responses[0].signal.aborted).toBe(true);
  await act(async () =>
    responses[1].resolve(new Response('{"items":["new"]}')),
  );
  await waitFor(() => expect(result.current.data?.items).toEqual(["new"]));
  await act(async () =>
    responses[0].resolve(new Response('{"items":["old"]}')),
  );
  expect(result.current.data?.items).toEqual(["new"]);
  unmount();
  expect(responses[1].signal.aborted).toBe(true);
});
it("bounds live invalidations, announces reconnect, and closes streams on pause", () => {
  class Stream {
    static current: Stream;
    onopen = () => {};
    onmessage = () => {};
    onerror = () => {};
    listeners: Record<string, () => void> = {};
    close = vi.fn();
    constructor() {
      Stream.current = this;
    }
    addEventListener(name: string, fn: () => void) {
      this.listeners[name] = fn;
    }
  }
  vi.stubGlobal("EventSource", Stream);
  const update = vi.fn();
  const { result, rerender } = renderHook(
    ({ enabled }) => useLive(enabled, update),
    { initialProps: { enabled: true } },
  );
  act(() => Stream.current.onopen());
  expect(result.current).toBe("Live");
  expect(update).toHaveBeenCalledTimes(1);
  act(() => {
    for (let i = 0; i < 100; i++) Stream.current.listeners.status();
  });
  expect(update).toHaveBeenCalledTimes(1);
  act(() => Stream.current.onerror());
  expect(result.current).toContain("Reconnecting");
  const stream = Stream.current;
  rerender({ enabled: false });
  expect(stream.close).toHaveBeenCalledOnce();
  expect(result.current).toBe("Paused");
});
