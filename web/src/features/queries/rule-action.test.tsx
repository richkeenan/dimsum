import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { InlineRuleAction } from "./rule-action";

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
        if (url === "/api/v1/settings")
          return Response.json({ status: activation, config: {} });
        if (url === "/api/v1/rules") return Response.json(activation);
        if (url === "/api/v1/rules/test")
          return Response.json({ decision: { result: decision } });
        throw new Error(`Unexpected request: ${url}`);
      }),
    );
    render(<InlineRuleAction name="ads.example" outcome={outcome!} />);
    const label = outcome === "blocked" ? "Allow" : "Block";
    fireEvent.click(screen.getByRole("button", { name: `${label} ads.example` }));
    if (notice) {
      expect(await screen.findByRole("status")).toHaveTextContent(notice);
    } else {
      await waitFor(() => {
        const button = screen.getByRole("button", { name: `${label} ads.example` });
        expect(button).toBeDisabled();
        expect(button).toHaveTextContent(label);
      });
      expect(screen.queryByRole("status")).not.toBeInTheDocument();
    }
  },
);
