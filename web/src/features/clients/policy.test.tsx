import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { PolicyEditor } from "./policy";
import { mergeClients } from "./model";

afterEach(() => vi.unstubAllGlobals());
let queryClient: QueryClient;
export const activation = {
  saved_revision: "r1",
  active_revision: "r1",
  active_generation: "1",
  pending: false,
  recovered: false,
  restart_required: false,
  sources: [],
};
export const policy = {
  status: activation,
  scope: "client",
  id: "tablet",
  desired: { id: "tablet", name: "Tablet" },
  effective: {
    id: "tablet",
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
  active: null,
};
function setup(
  fail = false,
  desired = policy.desired,
  onPromoted?: (id: string) => void,
  effective = policy.effective,
) {
  const writes: any[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      if (url.includes("preview"))
        return Response.json({
          changed_clients: ["tablet"],
          downloads_pending: [],
          network_changed: false,
          effective,
        });
      if (init?.method === "PATCH") {
        writes.push(JSON.parse(String(init.body)));
        return fail
          ? Response.json(
              {
                error: {
                  code: "revision_conflict",
                  message: "Configuration changed. Reload before saving.",
                },
              },
              { status: 409 },
            )
          : Response.json(activation);
      }
      if (url.includes("client-policy"))
        return Response.json({ ...policy, desired, effective });
      if (url.endsWith("catalog"))
        return Response.json({
          items: [
            {
              id: "adult",
              label: "Adult list",
              url: "https://example.com/adult",
              dialect: "dns-adblock",
              domain_kind: "suffix",
              available: true,
            },
          ],
        });
      return Response.json({
        status: activation,
        items: [{ id: "children", name: "Children" }],
      });
    }),
  );
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <PolicyEditor scope="client" id="tablet" onPromoted={onPromoted} />
    </QueryClientProvider>,
  );
  return writes;
}
it("saves an explicit equal-to-parent override sparsely and previews scope", async () => {
  const writes = setup();
  fireEvent.change(await screen.findByLabelText("Blocking"), {
    target: { value: "on" },
  });
  fireEvent.click(screen.getByLabelText("Changes only"));
  fireEvent.click(screen.getByRole("button", { name: /Save 1 change/ }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toEqual({
    revision: "r1",
    scope: "client",
    id: "tablet",
    fields: [{ path: ["blocking"], value: true }],
  });
});

it("retains a successful write activation failure when readback fails and retries only the read", async () => {
  setup();
  await screen.findByLabelText("Blocking");
  const previous = globalThis.fetch;
  let writes = 0,
    readable = false;
  const saved = {
    ...activation,
    saved_revision: "r2",
    pending: true,
    error: "Activation failed",
  };
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    if (init?.method === "PATCH") {
      writes++;
      return Response.json(saved);
    }
    if (url.includes("client-policy?"))
      return readable
        ? Response.json({ ...policy, status: saved })
        : Response.json(
            { error: { message: "Read unavailable" } },
            { status: 503 },
          );
    return previous(url, init);
  });
  fireEvent.change(screen.getByLabelText("Blocking"), {
    target: { value: "off" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save 1 change" }));
  const retry = await screen.findByRole(
    "button",
    { name: "Retry saved policy read" },
    { timeout: 4000 },
  );
  expect(
    screen.getByText(/Saved · activation failed: Activation failed/),
  ).toBeInTheDocument();
  expect(screen.getByLabelText("Blocking")).toHaveValue("off");
  expect(screen.getByRole("button", { name: "Save 1 change" })).toBeDisabled();
  readable = true;
  fireEvent.click(retry);
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Retry saved policy read" }),
    ).not.toBeInTheDocument(),
  );
  expect(writes).toBe(1);
});

it("resetting an unsaved route restores both inherited input buffers when no changes remain", async () => {
  setup(false, policy.desired, undefined, {
    ...policy.effective,
    upstream: {
      upstreams: ["192.0.2.53:53"],
      fallback_upstreams: ["192.0.2.54:53"],
    },
  } as any);
  fireEvent.change(await screen.findByLabelText("Primary servers"), {
    target: { value: "192.0.2.99:53" },
  });
  fireEvent.change(screen.getByLabelText("Fallback servers"), {
    target: { value: "192.0.2.98:53" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Reset upstream" }));
  expect(screen.getByLabelText("Primary servers")).toHaveValue("192.0.2.53:53");
  expect(screen.getByLabelText("Fallback servers")).toHaveValue(
    "192.0.2.54:53",
  );
  expect(screen.getByRole("button", { name: "Save 0 changes" })).toBeDisabled();
});

it("changes-only shows actual reset-all routes rules and pause without unrelated inherited settings", async () => {
  setup(false, {
    ...policy.desired,
    paused_until: "2026-09-23T18:00:00Z",
    overrides: {
      upstream: { upstreams: ["192.0.2.99:53"] },
      rules: [
        {
          id: "own",
          kind: "exact",
          action: "deny",
          pattern: "blocked.example",
          enabled: true,
        },
      ],
    },
  } as any);
  fireEvent.click(
    await screen.findByRole("button", { name: "Reset all overrides" }),
  );
  fireEvent.click(screen.getByLabelText("Changes only"));
  expect(
    screen.getByRole("heading", { name: "Upstream servers" }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("heading", { name: "Custom rules" }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("heading", { name: "Device pause" }),
  ).toBeInTheDocument();
  expect(screen.queryByLabelText("Blocking")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("heading", { name: "Filter lists" }),
  ).not.toBeInTheDocument();
});
it("assigns one profile without copying inherited policy", async () => {
  const writes = setup();
  await screen.findByRole("option", { name: "Children" });
  fireEvent.change(await screen.findByLabelText("Profile"), {
    target: { value: "children" },
  });
  fireEvent.click(screen.getByRole("button", { name: /Save 1 change/ }));
  await waitFor(() => expect(writes[0]?.profile).toBe("children"));
  expect(writes[0].fields).toBeUndefined();
});

it("changes-only keeps a name edit visible without unrelated profile or list controls", async () => {
  setup();
  fireEvent.change(await screen.findByLabelText("Name"), {
    target: { value: "Study tablet" },
  });
  fireEvent.click(screen.getByLabelText("Changes only"));
  expect(screen.getByLabelText("Name")).toHaveValue("Study tablet");
  expect(screen.queryByLabelText("Profile")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("heading", { name: "Filter lists" }),
  ).not.toBeInTheDocument();
});
it("subscribes and applies a catalogue list in one device-only transaction", async () => {
  const writes = setup();
  fireEvent.click(
    await screen.findByRole("button", { name: "Add Adult list" }),
  );
  fireEvent.click(screen.getByRole("button", { name: /Save 1 change/ }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0].scope).toBe("client");
  expect(writes[0].subscribe[0]).toEqual({
    id: "adult",
    url: "https://example.com/adult",
    dialect: "dns-adblock",
    domain_kind: "suffix",
    enabled: true,
  });
  expect(writes[0].fields).toEqual([{ path: ["lists", "adult"], value: true }]);
});
it("retains edits and offers reload after a conflict", async () => {
  setup(true);
  fireEvent.change(await screen.findByLabelText("Blocking"), {
    target: { value: "off" },
  });
  fireEvent.click(screen.getByRole("button", { name: /Save 1 change/ }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Settings changed",
  );
  expect(screen.getByLabelText("Blocking")).toHaveValue("off");
  expect(
    screen.getByRole("button", { name: "Reload saved policy" }),
  ).toBeEnabled();
});

it("surfaces a failed conflict reload without losing the draft or writing again", async () => {
  const writes = setup(true);
  fireEvent.change(await screen.findByLabelText("Blocking"), {
    target: { value: "off" },
  });
  fireEvent.click(screen.getByRole("button", { name: /Save 1 change/ }));
  await screen.findByRole("alert");
  const previous = globalThis.fetch;
  vi.stubGlobal("fetch", (url: string, init?: RequestInit) =>
    url.includes("client-policy?")
      ? Promise.resolve(
          Response.json(
            {
              error: {
                code: "unavailable",
                message: "Saved policy read unavailable",
              },
            },
            { status: 503 },
          ),
        )
      : previous(url, init),
  );
  fireEvent.click(screen.getByRole("button", { name: "Reload saved policy" }));
  expect(
    await screen.findByText(
      "Saved policy read unavailable",
      {},
      { timeout: 4000 },
    ),
  ).toBeVisible();
  expect(screen.getByLabelText("Blocking")).toHaveValue("off");
  expect(writes).toHaveLength(1);
});

it("keeps the opened revision and draft when a background refresh observes someone else’s edit", async () => {
  const writes = setup();
  fireEvent.change(await screen.findByLabelText("Blocking"), {
    target: { value: "off" },
  });
  const previous = globalThis.fetch;
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) =>
    url.includes("client-policy?")
      ? Response.json({
          ...policy,
          status: {
            ...activation,
            saved_revision: "r2",
            active_revision: "r2",
          },
        })
      : previous(url, init),
  );
  await queryClient.invalidateQueries({ queryKey: ["api", "client-policy"] });
  expect(
    await screen.findByText(/Newer settings are available/),
  ).toBeInTheDocument();
  expect(screen.getByLabelText("Blocking")).toHaveValue("off");
  fireEvent.click(screen.getByRole("button", { name: "Save 1 change" }));
  await waitFor(() => expect(writes[0]?.revision).toBe("r1"));
});
it("merges observations only by authoritative client ID, never by display name", () => {
  const rows = mergeClients({
    items: [{ policy_id: "a", name: "Same" }],
    observed: {
      items: [
        { address: "192.0.2.1", name: "Same", client_id: "" },
        { address: "192.0.2.2", client_id: "a" },
      ],
    },
  } as any);
  expect(rows).toHaveLength(2);
  expect(rows[0].observed.map((x) => x.address)).toEqual(["192.0.2.2"]);
  expect(rows[1].configured).toBeUndefined();
});

it("reset removes a saved explicit value instead of writing null or the parent value", async () => {
  const writes = setup(false, {
    ...policy.desired,
    overrides: { blocking: true },
  } as typeof policy.desired);
  fireEvent.click(
    await screen.findByRole("button", { name: "Reset Blocking" }),
  );
  expect(screen.getByLabelText("Blocking")).toHaveValue("inherit");
  fireEvent.click(screen.getByRole("button", { name: "Save 1 change" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0].fields).toEqual([{ path: ["blocking"], reset: true }]);
});

it("relink replaces selectors while keeping stable identity and policy", async () => {
  const writes = setup();
  fireEvent.change(await screen.findByLabelText("Addresses"), {
    target: { value: "192.0.2.20\n2001:db8::20" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Stage selector replacement" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Save 1 change" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toEqual({
    revision: "r1",
    scope: "client",
    id: "tablet",
    selectors: {
      addresses: ["192.0.2.20", "2001:db8::20"],
      macs: [],
      cidrs: [],
    },
  });
});

it("promotes a legacy identity and follows the new stable detail instead of reloading its obsolete ID", async () => {
  const promoted = vi.fn();
  const writes = setup(
    false,
    { name: "Tablet", address: "192.0.2.10" } as any,
    promoted,
  );
  fireEvent.change(
    await screen.findByLabelText("Stable device ID (required to save policy)"),
    { target: { value: "stable-tablet" } },
  );
  fireEvent.click(screen.getByRole("button", { name: "Save 1 change" }));
  await waitFor(() => expect(promoted).toHaveBeenCalledWith("stable-tablet"));
  expect(writes[0]).toEqual({
    revision: "r1",
    scope: "client",
    id: "tablet",
    promote_id: "stable-tablet",
  });
});
