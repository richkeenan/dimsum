import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import {
  api,
  APIError,
  type Activation,
  type DHCPConfigResponse,
  type DHCPStatusResponse,
} from "@/lib/api";
import DHCP, { DHCPForm, DHCPState } from "./index";
import { Reservations, Leases, DHCPCheck } from "./operations";

const activation: Activation = {
  saved_revision: "one",
  active_revision: "one",
  active_generation: "1",
  pending: false,
  recovered: false,
  restart_required: false,
  sources: [],
};
const initial: DHCPConfigResponse = {
  status: activation,
  config: {
    enabled: false,
    interface: "",
    server_ip: "",
    subnet: "",
    gateway: "",
    range_start: "",
    range_end: "",
    lease_seconds: 0,
    local_domain: "",
    max_leases: 1024,
    reservations: [],
  },
};
const unavailable: DHCPStatusResponse = {
  status: activation,
  dhcp: null,
  runtime_available: false,
};
afterEach(() => vi.restoreAllMocks());
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}
function resource(ui: React.ReactNode, config?: DHCPConfigResponse) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  if (config) client.setQueryData(["api", "dhcp", "dhcp"], config);
  return {
    client,
    ...render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>),
  };
}

it("saves incomplete disabled configuration without silently enabling and preserves conflict drafts", async () => {
  const send = vi
    .spyOn(api, "send")
    .mockRejectedValue(new APIError(409, "revision_conflict", "stale"));
  const get = vi.spyOn(api, "get").mockResolvedValue({
    ...initial,
    status: { ...activation, saved_revision: "two" },
  });
  const view = resource(
    <DHCPForm applied={unavailable} refresh={() => {}} />,
    initial,
  );
  expect(screen.getByLabelText("Lease duration (seconds)")).toBeValid();
  expect(screen.getByLabelText("Enable DHCPv4")).not.toBeChecked();
  await userEvent.type(screen.getByLabelText("LAN interface"), "eth0");
  await userEvent.click(
    screen.getByRole("button", { name: "Save DHCP settings" }),
  );
  expect(send).toHaveBeenCalledWith("dhcp", "PATCH", {
    revision: "one",
    edits: [{ path: ["interface"], value: "eth0" }],
  });
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Settings changed",
  );
  await act(async () => {
    view.client.setQueryData(["api", "dhcp", "dhcp"], {
      ...initial,
      status: { ...activation, saved_revision: "two" },
    });
  });
  expect(screen.getByLabelText("LAN interface")).toHaveValue("eth0");
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Save DHCP settings" }),
    ).toBeDisabled(),
  );
  await userEvent.click(
    screen.getByRole("button", { name: "Reload saved settings" }),
  );
  expect(get).toHaveBeenCalledWith("dhcp", expect.any(AbortSignal));
  expect(screen.getByLabelText("LAN interface")).toHaveValue("");
});

it.each(["held", "failed"])(
  "installs reload before immediate editing despite an old poll and a %s background fetch",
  async (background) => {
    const key = ["api", "dhcp", "dhcp"];
    const client = new QueryClient();
    client.setQueryData(key, initial);
    const updated: DHCPConfigResponse = {
      status: { ...activation, saved_revision: "two" },
      config: {
        ...initial.config,
        interface: "fresh0",
        local_domain: "home.arpa",
      },
    };
    const oldPoll = deferred<DHCPConfigResponse>();
    const reload = deferred<DHCPConfigResponse>();
    const laterPoll = deferred<DHCPConfigResponse>();
    const signals: (AbortSignal | undefined)[] = [];
    let calls = 0;
    vi.spyOn(api, "get").mockImplementation(async (path, signal) => {
      if (path === "dhcp") {
        signals.push(signal);
        return (
          [oldPoll.promise, reload.promise, laterPoll.promise][calls++] ??
          updated
        );
      }
      if (path === "dhcp/status") return unavailable;
      return { status: activation, items: [], runtime_available: false };
    });
    const send = vi
      .spyOn(api, "send")
      .mockRejectedValueOnce(new APIError(409, "revision_conflict", "stale"))
      .mockResolvedValue(updated.status);
    const view = render(
      <QueryClientProvider client={client}>
        <DHCP />
      </QueryClientProvider>,
    );
    await waitFor(() =>
      expect(screen.getByLabelText("LAN interface")).toBeEnabled(),
    );
    await userEvent.type(screen.getByLabelText("LAN interface"), "draft0");
    await userEvent.click(
      screen.getByRole("button", { name: "Save DHCP settings" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Settings changed",
    );

    // A periodic refetch began before reload and could return revision one late.
    let oldRequest!: Promise<void>;
    act(() => {
      oldRequest = client.invalidateQueries({ queryKey: key, exact: true });
    });
    await waitFor(() => expect(calls).toBe(1));
    await userEvent.click(
      screen.getByRole("button", { name: "Reload saved settings" }),
    );
    expect(screen.getByLabelText("LAN interface")).toBeDisabled();
    expect(screen.getByLabelText("LAN interface")).toHaveValue("draft0");
    await act(async () => {
      reload.resolve(updated);
      await reload.promise;
    });
    await waitFor(() =>
      expect(screen.getByLabelText("LAN interface")).toBeEnabled(),
    );
    expect(screen.getByLabelText("LAN interface")).toHaveValue("fresh0");
    expect(screen.getByLabelText("Local domain")).toHaveValue("home.arpa");
    expect(client.getQueryData(key)).toEqual(updated);
    expect(signals[0]?.aborted).toBe(true);
    expect(
      screen.getByRole("button", { name: "Save DHCP settings" }),
    ).toBeDisabled();
    expect(calls).toBe(2); // Reload must not start a second invalidation/refetch.
    await act(async () => {
      oldPoll.resolve(initial);
      await oldRequest;
    });
    expect(client.getQueryData(key)).toEqual(updated);

    let laterRequest!: Promise<void>;
    act(() => {
      laterRequest = client.invalidateQueries({ queryKey: key, exact: true });
    });
    await waitFor(() => expect(calls).toBe(3));
    if (background === "failed") {
      await act(async () => {
        laterPoll.reject(
          new APIError(400, "bad_request", "Background read failed"),
        );
        await laterRequest;
      });
    }
    await userEvent.clear(screen.getByLabelText("LAN interface"));
    await userEvent.type(screen.getByLabelText("LAN interface"), "next0");
    await userEvent.click(
      screen.getByRole("button", { name: "Save DHCP settings" }),
    );
    expect(send).toHaveBeenLastCalledWith("dhcp", "PATCH", {
      revision: "two",
      edits: [{ path: ["interface"], value: "next0" }],
    });
    view.unmount();
    client.clear();
    if (background === "held") {
      laterPoll.resolve(updated);
      await laterRequest;
    }
  },
);

it("requires complete enable fields and keeps topology locked until disable is applied", async () => {
  resource(<DHCPForm applied={unavailable} refresh={() => {}} />, initial);
  await userEvent.click(screen.getByLabelText("Enable DHCPv4"));
  expect(screen.getByLabelText("Static server IPv4 address")).toBeRequired();
  expect(screen.getByLabelText("Static server IPv4 address")).toBeInvalid();
});
it("retains the draft on reload failure and leaves successful reloads clean for later polls", async () => {
  const newer = {
    ...initial,
    status: { ...activation, saved_revision: "two" },
    config: { ...initial.config, interface: "fresh0" },
  };
  vi.spyOn(api, "get")
    .mockRejectedValueOnce(new APIError(400, "bad_request", "Reload failed"))
    .mockResolvedValue(newer);
  const send = vi.spyOn(api, "send").mockResolvedValue(activation);
  const { client } = resource(
    <DHCPForm applied={unavailable} refresh={() => {}} />,
    initial,
  );
  await userEvent.type(screen.getByLabelText("LAN interface"), "draft0");
  await userEvent.click(
    screen.getByRole("button", { name: "Reload saved settings" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent("Reload failed");
  expect(screen.getByLabelText("LAN interface")).toHaveValue("draft0");
  await userEvent.click(
    screen.getByRole("button", { name: "Reload saved settings" }),
  );
  await waitFor(() =>
    expect(screen.getByLabelText("LAN interface")).toHaveValue("fresh0"),
  );
  await act(async () => {
    client.setQueryData(["api", "dhcp", "dhcp"], {
      ...newer,
      status: { ...activation, saved_revision: "three" },
      config: { ...newer.config, interface: "polled0" },
    });
  });
  await waitFor(() =>
    expect(screen.getByLabelText("LAN interface")).toHaveValue("polled0"),
  );
  await userEvent.type(screen.getByLabelText("LAN interface"), "1");
  await userEvent.click(
    screen.getByRole("button", { name: "Save DHCP settings" }),
  );
  expect(send).toHaveBeenCalledWith("dhcp", "PATCH", {
    revision: "three",
    edits: [{ path: ["interface"], value: "polled01" }],
  });
});
it("unchecking enabled does not unlock topology before a saved disable", async () => {
  resource(<DHCPForm refresh={() => {}} />, {
    ...initial,
    config: { ...initial.config, enabled: true },
  });
  await userEvent.click(screen.getByLabelText("Enable DHCPv4"));
  expect(screen.getByLabelText("LAN interface")).toBeDisabled();
});

it("shows desired/applied lag and actionable ownership failures without reporting success", () => {
  const value: DHCPStatusResponse = {
    status: { ...activation, pending: true },
    runtime_available: true,
    dhcp: {
      state: "degraded",
      desired_generation: "3",
      applied_generation: "2",
      pending_generation: "3",
      desired_enabled: true,
      applied_enabled: true,
      desired_interface: "eth0",
      desired_server_ip: "192.0.2.2",
      interface: "eth0",
      server_ip: "192.0.2.2",
      last_error:
        "192.0.2.100 owned by 02:00:00:00:00:10 until 2026-09-23T12:00:00Z",
      runtime: {
        generation: "2",
        storage: "healthy",
        capacity: "1024",
        held: "1",
        pending: "0",
        clock_suspended: false,
        transport: {},
      },
    },
  };
  render(<DHCPState value={value} />);
  expect(screen.getByRole("status")).toHaveTextContent("Pending generation 3");
  expect(screen.getByRole("alert")).toHaveTextContent("192.0.2.100 owned by");
  expect(screen.getByText(/Enabled · generation 2/)).toBeVisible();
});

it("reservation identity switch uses grouped edits; failure keeps the form and never deletes a lease", async () => {
  vi.spyOn(api, "get").mockResolvedValue({
    status: activation,
    items: [
      {
        id: "printer",
        address: "192.0.2.20",
        mac: "02:00:00:00:00:10",
        hostname: "printer",
      },
    ],
  });
  const send = vi
    .spyOn(api, "send")
    .mockRejectedValue(
      new APIError(
        422,
        "invalid_configuration",
        "Address is owned until tomorrow",
      ),
    );
  resource(<Reservations tick={0} refresh={() => {}} />);
  await userEvent.click(
    await screen.findByRole("button", { name: "Edit reservation printer" }),
  );
  await userEvent.selectOptions(
    screen.getByLabelText("Identity type"),
    "client_id",
  );
  await userEvent.type(screen.getByLabelText("Client ID (hex)"), "aabb");
  await userEvent.click(
    screen.getByRole("button", { name: "Save reservation" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Address is owned",
  );
  expect(screen.getByLabelText("Client ID (hex)")).toHaveValue("aabb");
  expect(send).toHaveBeenCalledWith("dhcp/reservations/printer", "PATCH", {
    revision: "one",
    edits: [
      { path: ["address"], value: "192.0.2.20" },
      { path: ["mac"], value: "" },
      { path: ["client_id"], value: "aabb" },
      { path: ["hostname"], value: "printer" },
    ],
  });
});

it("reservation create and delete failures retain input and require an explicit retry", async () => {
  vi.spyOn(api, "get").mockResolvedValue({
    status: activation,
    items: [{ id: "printer", address: "192.0.2.20", mac: "02:00:00:00:00:10" }],
  });
  const send = vi
    .spyOn(api, "send")
    .mockRejectedValue(new Error("Write unavailable"));
  resource(<Reservations tick={0} refresh={() => {}} />);
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Add reservation" }),
    ).toBeEnabled(),
  );
  await userEvent.click(
    screen.getByRole("button", { name: "Add reservation" }),
  );
  await userEvent.type(screen.getByLabelText("Reservation ID"), "camera");
  await userEvent.type(
    screen.getByLabelText("Reserved IPv4 address"),
    "192.0.2.21",
  );
  await userEvent.type(
    screen.getByLabelText("MAC address"),
    "02:00:00:00:00:11",
  );
  await userEvent.click(
    screen.getByRole("button", { name: "Save reservation" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Write unavailable",
  );
  expect(screen.getByLabelText("Reservation ID")).toHaveValue("camera");
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
  await userEvent.click(
    screen.getByRole("button", { name: "Remove reservation printer" }),
  );
  expect(
    screen.getByText(/Lease ownership and expiry remain intact/),
  ).toBeVisible();
  await userEvent.click(
    screen.getByRole("button", { name: "Confirm removal" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Write unavailable",
  );
  expect(send).toHaveBeenLastCalledWith("dhcp/reservations/printer", "DELETE", {
    revision: "one",
  });
});

it("invalidates obsolete lease cursors instead of rendering old rows as the next page", async () => {
  const get = vi.spyOn(api, "get").mockImplementation(async (path) => {
    if (path.includes("cursor="))
      throw new APIError(409, "lease_cursor_expired", "restart");
    return { runtime_available: false, items: [], next_cursor: "opaque" };
  });
  resource(<Leases tick={0} />);
  await screen.findByText(/Live lease inspection is unavailable/);
  await userEvent.click(screen.getByRole("button", { name: "Next leases" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Lease ownership changed",
  );
  expect(screen.getByRole("button", { name: "Next leases" })).toBeDisabled();
  await userEvent.click(
    screen.getByRole("button", { name: "Restart lease pagination" }),
  );
  await screen.findByText("Page 1 · up to 100 leases");
  await waitFor(() =>
    expect(get.mock.calls.at(-1)?.[0]).toBe("dhcp/leases?limit=100"),
  );
  await userEvent.type(
    screen.getByLabelText("Filter lease IPv4 address"),
    "192.0.2.20",
  );
  await waitFor(() =>
    expect(get.mock.calls.at(-1)?.[0]).toBe(
      "dhcp/leases?limit=100&address=192.0.2.20",
    ),
  );
});

it("uses the shared explicit job with active discovery off by default", async () => {
  vi.spyOn(api, "get").mockResolvedValue({ items: [] });
  const send = vi.spyOn(api, "send").mockResolvedValue({
    id: "check",
    kind: "dhcp-check",
    state: "failed",
    error: "Static address required",
  });
  resource(<DHCPCheck />);
  await userEvent.click(screen.getByRole("button", { name: "Run DHCP check" }));
  expect(send).toHaveBeenCalledWith("jobs", "POST", {
    kind: "dhcp-check",
    input: { probe_other_servers: false, timeout_ms: 1000 },
  });
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Static address required",
  );
});
