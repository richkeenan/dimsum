import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { api } from "@/lib/api";
import { DomainInspector } from "./inspect";
import type { Schema } from "./model";

vi.mock("@/lib/hooks", () => ({
  useResource: () => ({
    data: { items: [{ policy_id: "tablet", name: "Study tablet" }] },
    loading: false,
  }),
}));
afterEach(() => vi.restoreAllMocks());

const explanation: Schema["ClientPolicyExplanation"] = {
  name: "ads.example.test",
  normalized: "ads.example.test",
  generation: "42",
  client_id: "",
  matching_method: "network",
  handling: "policy",
  effective: {
    id: "",
    profile_id: "",
    blocking: { value: true, source: {} },
    filtering: true,
    paused_until: "0001-01-01T00:00:00Z",
    global_paused: false,
    lists: {},
    upstream_source: {},
    route_id: "",
    upstream: { upstreams: [] },
    override_count: 0,
    rules: [],
  },
  decision: { result: "forward", generation: "42", rule_id: "", source_ids: [], scope: {} },
};

it.each([
  ["forward", "Not blocked by current rules"],
  ["allow", "Allowed by a matching rule"],
  ["block", "Blocked by current rules"],
  ["local", "Answered by local DNS"],
  ["paused", "Filtering is paused"],
] as const)("explains a %s decision without presenting an access test", async (result, label) => {
  vi.spyOn(api, "send").mockResolvedValue({
    ...explanation,
    decision: { ...explanation.decision, result },
  });
  render(<DomainInspector />);
  fireEvent.change(screen.getByLabelText("Domain"), { target: { value: explanation.name } });
  fireEvent.click(screen.getByRole("button", { name: "Check domain" }));
  expect(await screen.findByRole("status")).toHaveTextContent(label);
  expect(screen.getByText(/does not test whether the website is reachable/)).toBeVisible();
});

it("keeps the selected device and matched rule inspectable without exposing internal IDs upfront", async () => {
  const send = vi.spyOn(api, "send").mockResolvedValue({
    ...explanation,
    client_id: "tablet",
    matching_method: "configured_id",
    decision: {
      ...explanation.decision,
      result: "block",
      rule_id: "internal-rule",
      source_ids: ["list-source"],
      scope: { kind: "client", id: "tablet" },
    },
  });
  render(<DomainInspector clientID="tablet" />);
  fireEvent.change(screen.getByLabelText("Domain"), { target: { value: explanation.name } });
  fireEvent.click(screen.getByRole("button", { name: "Check domain" }));
  expect(await screen.findByRole("status")).toHaveTextContent("Study tablet");
  expect(send).toHaveBeenCalledWith("rules/test", "POST", {
    name: explanation.name,
    client_id: "tablet",
  });
  expect(screen.queryByText(/internal-rule/)).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Domain check details" }));
  expect(within(screen.getByRole("dialog")).getByText(/internal-rule/)).toBeVisible();
  expect(screen.getByRole("dialog")).toHaveTextContent("list-source");
});

it("distinguishes disabled filtering from a temporary pause", async () => {
  vi.spyOn(api, "send").mockResolvedValue({
    ...explanation,
    effective: {
      ...explanation.effective,
      blocking: { value: false, source: {} },
      filtering: false,
    },
    decision: { ...explanation.decision, result: "paused" },
  });
  render(<DomainInspector />);
  fireEvent.change(screen.getByLabelText("Domain"), { target: { value: explanation.name } });
  fireEvent.click(screen.getByRole("button", { name: "Check domain" }));
  expect(await screen.findByRole("status")).toHaveTextContent("Filtering is turned off");
});

it("does not call a winning custom rule a filter list when both sources match", async () => {
  vi.spyOn(api, "send").mockResolvedValue({
    ...explanation,
    decision: {
      ...explanation.decision,
      result: "allow",
      rule_id: "custom:manual-allow",
      source_ids: ["custom", "privacy-list"],
    },
    effective: {
      ...explanation.effective,
      rules: [
        {
          source: {},
          rule: {
            id: "manual-allow",
            kind: "exact",
            pattern: explanation.name,
            action: "allow",
            enabled: true,
          },
        },
      ],
    },
  });
  render(<DomainInspector />);
  fireEvent.change(screen.getByLabelText("Domain"), { target: { value: explanation.name } });
  fireEvent.click(screen.getByRole("button", { name: "Check domain" }));
  const status = await screen.findByRole("status");
  expect(status).toHaveTextContent("Allowed by a matching rule");
  expect(status).not.toHaveTextContent("filter list");
});
