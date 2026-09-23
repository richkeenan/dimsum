import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useLive, useResource } from "./hooks";

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}
let client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
afterEach(() => {
  client.clear();
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

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
  const { result, rerender } = renderHook(({ path }) => useResource<{ items: string[] }>(path), {
    wrapper,
    initialProps: { path: "queries?name=first" },
  });
  rerender({ path: "queries?name=second" });
  expect(responses[0].signal.aborted).toBe(true);
  await act(async () => responses[1].resolve(new Response('{"items":["new"]}')));
  await waitFor(() => expect(result.current.data?.items).toEqual(["new"]));
  await act(async () => responses[0].resolve(new Response('{"items":["old"]}')));
  expect(result.current.data?.items).toEqual(["new"]);
});

it("deduplicates shared settings and retains data during a background refresh", async () => {
  const fetcher = vi.fn().mockResolvedValue(new Response('{"revision":"one"}'));
  vi.stubGlobal("fetch", fetcher);
  const { result, rerender } = renderHook(
    ({ tick }) => [
      useResource<{ revision: string }>("settings", tick),
      useResource<{ revision: string }>("settings", tick),
    ],
    { wrapper, initialProps: { tick: 0 } },
  );
  await waitFor(() => expect(result.current[0].data?.revision).toBe("one"));
  expect(fetcher).toHaveBeenCalledTimes(1);
  fetcher.mockImplementation(() => new Promise(() => {}));
  rerender({ tick: 1 });
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  expect(result.current[0].data?.revision).toBe("one");
  expect(result.current[0].loading).toBe(false);
});

it("polls live results every two seconds and stops on pause", () => {
  vi.useFakeTimers();
  const update = vi.fn();
  const { result, rerender } = renderHook(({ enabled }) => useLive(enabled, update), {
    initialProps: { enabled: true },
  });
  act(() => vi.advanceTimersByTime(6000));
  expect(update).toHaveBeenCalledTimes(3);
  expect(result.current).toBe("Live");
  rerender({ enabled: false });
  act(() => vi.advanceTimersByTime(6000));
  expect(update).toHaveBeenCalledTimes(3);
  expect(result.current).toBe("Paused");
});
