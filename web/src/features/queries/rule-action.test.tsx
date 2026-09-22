import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { InlineRuleAction } from "./rule-action";

afterEach(() => vi.unstubAllGlobals());

it.each([
  ["block", "Block rule active"],
  ["allow", "saved, but an allow exception still takes precedence."],
])(
  "checks the active policy after a flat activation response: %s",
  async (decision, notice) => {
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
        if (url === "/api/v1/settings")
          return Response.json({ status: activation, config: {} });
        if (url === "/api/v1/rules") return Response.json(activation);
        if (url === "/api/v1/rules/test")
          return Response.json({ decision: { result: decision } });
        throw new Error(`Unexpected request: ${url}`);
      }),
    );
    render(<InlineRuleAction name="ads.example" outcome="forwarded" />);
    fireEvent.click(screen.getByRole("button", { name: "Block ads.example" }));
    expect(await screen.findByRole("status")).toHaveTextContent(notice);
  },
);
