import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { InlineRuleAction } from "./rule-action";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

it.each(["matched", "unmatched", "unavailable"])(
  "resolves device scope authoritatively when observations are missing: %s",
  async (mode) => {
    const writes: any[] = [];
    const status = {
      saved_revision: "r1",
      active_revision: "r1",
      active_generation: "1",
      pending: false,
      recovered: false,
      restart_required: false,
      sources: [],
    };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init?: RequestInit) => {
        if (url.endsWith("settings")) return Response.json({ status, config: {} });
        if (url.endsWith("profiles")) return Response.json({ items: [] });
        if (url.includes("client-policy?"))
          return Response.json({
            scope: "client",
            id: "stable",
            desired: { id: "stable" },
            status,
          });
        if (url.endsWith("rules/test"))
          return mode === "unavailable"
            ? Response.json({ error: { message: "Identity service unavailable" } }, { status: 503 })
            : Response.json({
                client_id: mode === "matched" ? "stable" : "",
                decision: { result: "allow" },
              });
        if (url.endsWith("client-policy")) {
          writes.push(JSON.parse(String(init?.body)));
          return Response.json(status);
        }
        throw new Error(`Unexpected inventory dependency: ${url}`);
      }),
    );
    render(
      <QueryClientProvider
        client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
      >
        <InlineRuleAction name="ads.example" outcome="blocked" address="192.0.2.12" />
      </QueryClientProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Allow ads.example" }));
    expect(writes).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Create allow rule" }));
    if (mode === "matched")
      await waitFor(() => expect(writes[0]).toMatchObject({ scope: "client", id: "stable" }));
    else {
      expect(await screen.findByRole("alert")).toHaveTextContent(
        mode === "unmatched" ? "Choose a profile for this device" : "Identity service unavailable",
      );
      expect(writes).toHaveLength(0);
    }
  },
);

afterEach(() => vi.unstubAllGlobals());

it.each([
  ["forwarded", "block", undefined],
  ["blocked", "allow", undefined],
  ["forwarded", "allow", "saved, but an allow exception still takes precedence."],
])(
  "checks the active policy after a flat activation response: %s",
  async (outcome, decision, notice) => {
    const activation = {
      saved_revision: "saved",
      active_revision: "saved",
      active_generation: "2",
      pending: false,
      recovered: false,
      restart_required: false,
      sources: [],
    };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url === "/api/v1/settings") return Response.json({ status: activation, config: {} });
        if (url === "/api/v1/rules") return Response.json(activation);
        if (url === "/api/v1/rules/test") return Response.json({ decision: { result: decision } });
        throw new Error(`Unexpected request: ${url}`);
      }),
    );
    render(<InlineRuleAction name="ads.example" outcome={outcome!} />);
    const label = outcome === "blocked" ? "Allow" : "Block";
    fireEvent.click(screen.getByRole("button", { name: `${label} ads.example` }));
    fireEvent.click(
      screen.getByRole("button", {
        name: `Create ${label.toLowerCase()} rule`,
      }),
    );
    if (notice) {
      expect(await screen.findByRole("status")).toHaveTextContent(notice);
    } else {
      await waitFor(() => {
        const button = screen.getByRole("button", {
          name: `Create ${label.toLowerCase()} rule`,
        });
        expect(button).toBeDisabled();
      });
      expect(screen.getByRole("status")).toHaveTextContent("rule active");
    }
  },
);
