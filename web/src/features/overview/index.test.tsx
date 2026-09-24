import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import type { Rankings, Summary } from "@/lib/api";
import Overview from ".";

afterEach(() => vi.unstubAllGlobals());

it("shows the uncapped active-client count without fetching the device inventory", async () => {
  const range = { from: "2026-09-17T21:51:57.542Z", to: "2026-09-24T21:51:57.542Z" };
  const meta = { range, complete: true, updated_at: range.to };
  const summary: Summary = {
    ...meta,
    queries: "500",
    blocked: "0",
    fresh: "10",
    stale: "0",
    rejected: "0",
    duration_us: "1000",
  };
  const rankings: Rankings = {
    ...meta,
    active_clients: "205",
    clients: [],
    domains: [],
  };
  const requested: string[] = [];
  vi.stubGlobal("fetch", (url: string) => {
    const resource = url.split("?")[0].split("/").at(-1)!;
    requested.push(resource);
    if (resource === "summary") return Promise.resolve(Response.json(summary));
    if (resource === "rankings") return Promise.resolve(Response.json(rankings));
    // Other panels can remain in flight independently of the count card.
    return new Promise<Response>(() => {});
  });
  const client = new QueryClient();
  const { unmount } = render(
    <QueryClientProvider client={client}>
      <Overview
        range={new URLSearchParams(range).toString()}
        resolution={3600}
        drill={() => {}}
        onPerformance={() => {}}
      />
    </QueryClientProvider>,
  );
  try {
    await waitFor(() =>
      expect(screen.getByText("Active clients").parentElement).toHaveTextContent("205"),
    );
    expect(requested).not.toContain("clients");
  } finally {
    unmount();
    client.clear();
  }
});
