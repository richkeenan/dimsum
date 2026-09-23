import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { api, type Job } from "@/lib/api";
import { DHCPCheck, Leases, Reservations } from "./operations";

afterEach(() => vi.restoreAllMocks());

function resource(ui: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

it("retains lease sorting across the loading state of a new address filter", async () => {
  const data = {
    runtime_available: true,
    items: [
      { address: "192.0.2.2", hostname: "Alpha", state: "bound", expiry: "2099-06-01T12:30:00Z" },
      { address: "192.0.2.10", hostname: "Beta", state: "bound", expiry: "2099-06-01T12:30:00Z" },
    ],
  };
  let finish!: (value: typeof data) => void;
  vi.spyOn(api, "get").mockImplementation((path) =>
    path.includes("address=")
      ? (new Promise((resolve) => {
          finish = resolve;
        }) as never)
      : (Promise.resolve(data) as never),
  );
  resource(<Leases tick={0} />);
  await screen.findByText("Alpha");
  fireEvent.click(screen.getByRole("button", { name: "IP address" }));
  fireEvent.click(screen.getByRole("button", { name: "IP address" }));
  fireEvent.change(screen.getByLabelText("Filter by IP address"), {
    target: { value: "192.0.2.10" },
  });
  await screen.findByText("Loading addresses…");
  await act(async () => finish({ ...data, items: [data.items[1]] }));
  await screen.findByText("Beta");
  expect(screen.getByRole("columnheader", { name: "IP address" })).toHaveAttribute(
    "aria-sort",
    "descending",
  );
});

it("keeps the reservation name as the API identifier and saves the current revision", async () => {
  vi.spyOn(api, "get").mockResolvedValue({
    status: { saved_revision: "current" },
    items: [],
  });
  const send = vi.spyOn(api, "send").mockResolvedValue({});
  resource(<Reservations tick={0} refresh={() => {}} />);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Add reservation" })).toBeEnabled(),
  );
  await userEvent.click(screen.getByRole("button", { name: "Add reservation" }));
  await userEvent.type(screen.getByLabelText("Reservation name"), "office-printer");
  await userEvent.type(screen.getByLabelText("IP address"), "192.0.2.20");
  await userEvent.type(screen.getByLabelText("MAC address"), "02:00:00:00:00:20");
  expect(screen.getByLabelText("Identify device by")).toHaveValue("mac");
  await userEvent.click(screen.getByRole("button", { name: "Save reservation" }));
  expect(send).toHaveBeenCalledWith("dhcp/reservations", "POST", {
    revision: "current",
    item: {
      id: "office-printer",
      address: "192.0.2.20",
      mac: "02:00:00:00:00:20",
      hostname: "",
    },
  });
  await waitFor(() => expect(screen.queryByLabelText("Reservation name")).not.toBeInTheDocument());
});

it("distinguishes active leases from expired and conflict holds, keeping internals in row details", async () => {
  const expiry = "2099-06-01T12:30:00Z";
  vi.spyOn(api, "get").mockResolvedValue({
    runtime_available: true,
    items: [
      {
        address: "192.0.2.10",
        hostname: "printer",
        mac: "02:00:00:00:00:10",
        state: "bound",
        expiry,
        hold_until: "2099-06-02T12:30:00Z",
        client_id: "01020000000010",
      },
      {
        address: "192.0.2.11",
        hostname: "speaker",
        mac: "02:00:00:00:00:11",
        state: "quarantined",
        expiry,
        hold_until: expiry,
      },
      {
        address: "192.0.2.12",
        hostname: "tablet",
        mac: "02:00:00:00:00:12",
        state: "bound",
        expiry: "2020-01-01T00:00:00Z",
        hold_until: expiry,
      },
    ],
  });
  resource(<Leases tick={0} />);
  const printer = (await screen.findByText("printer")).closest("tr")!;
  expect(within(printer).getByText("Active")).toBeVisible();
  expect(
    within(printer).getByText(
      new Date(expiry).toLocaleString(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      }),
    ),
  ).toBeVisible();
  expect(screen.getByText("Held after conflict")).toBeVisible();
  expect(screen.getByText("Expired · address held")).toBeVisible();
  expect(screen.getByText("01020000000010")).not.toBeVisible();
  await userEvent.click(screen.getByLabelText("Address details for 192.0.2.10"));
  expect(screen.getByText("01020000000010")).toBeVisible();
  expect(screen.queryByRole("button", { name: "Next addresses" })).not.toBeInTheDocument();
});

it("explains an unavailable address list without declaring addresses free", async () => {
  vi.spyOn(api, "get").mockResolvedValue({
    runtime_available: false,
    items: [],
  });
  resource(<Leases tick={0} />);
  expect(await screen.findByText("No live address list available.")).toBeVisible();
  expect(screen.getByRole("status")).toHaveTextContent("Devices may still have unexpired leases.");
  expect(screen.queryByText(/No leased addresses yet/)).not.toBeInTheDocument();
});

it("runs checks only on request with the server search off, and does not equate job completion with readiness", async () => {
  const job: Job = {
    id: "check-1",
    kind: "dhcp-check",
    created: "2026-01-01T00:00:00Z",
    state: "succeeded",
    result: {
      checks: {
        configuration: "valid",
        dns_ready: "false",
        interface_static_address_socket: "unavailable",
      },
      probe_requested: false,
      observation: "not_probed",
    },
  };
  vi.spyOn(api, "get").mockResolvedValue({ items: [] });
  const send = vi.spyOn(api, "send").mockResolvedValue(job);
  resource(<DHCPCheck />);
  expect(screen.getByText("Troubleshooting").closest("details")).not.toHaveAttribute("open");
  expect(send).not.toHaveBeenCalled();
  await userEvent.click(screen.getByText("Troubleshooting"));
  expect(screen.getByRole("checkbox", { name: "Look for other DHCP servers" })).not.toBeChecked();
  await userEvent.click(screen.getByRole("button", { name: "Check setup" }));
  expect(send).toHaveBeenCalledWith("jobs", "POST", {
    kind: "dhcp-check",
    input: { probe_other_servers: false, timeout_ms: 1000 },
  });
  expect(await screen.findByText("Setup check finished")).toBeVisible();
  expect(screen.getByText("Not ready")).toBeVisible();
  expect(screen.getByText("unavailable")).toBeVisible();
  expect(screen.getByText("Technical details").closest("details")).not.toHaveAttribute("open");
});

it.each([
  ["failed", "The search could not finish."],
  [
    "no_offer_observed",
    "No other servers responded during this check. A quiet server may still be present.",
  ],
])("shows the actual %s server-search outcome", async (observation, message) => {
  vi.spyOn(api, "get").mockResolvedValue({ items: [] });
  vi.spyOn(api, "send").mockResolvedValue({
    id: "check-2",
    kind: "dhcp-check",
    state: observation === "failed" ? "failed" : "succeeded",
    error: observation === "failed" ? "port unavailable" : undefined,
    result: { probe_requested: true, observation, other_servers: [] },
  });
  resource(<DHCPCheck />);
  await userEvent.click(screen.getByText("Troubleshooting"));
  await userEvent.click(screen.getByRole("checkbox", { name: "Look for other DHCP servers" }));
  await userEvent.click(screen.getByRole("button", { name: "Check setup" }));
  expect(await screen.findByText(message)).toBeVisible();
  if (observation === "failed")
    expect(screen.getByRole("alert")).toHaveTextContent("port unavailable");
});

it("keeps check controls disabled while the submitted job is running", async () => {
  vi.spyOn(api, "get").mockResolvedValue({ items: [] });
  vi.spyOn(api, "send").mockResolvedValue({
    id: "running-check",
    kind: "dhcp-check",
    state: "running",
  });
  resource(<DHCPCheck />);
  await userEvent.click(screen.getByText("Troubleshooting"));
  await userEvent.click(screen.getByRole("button", { name: "Check setup" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Checking setup…" })).toBeDisabled(),
  );
  expect(screen.getByRole("checkbox", { name: "Look for other DHCP servers" })).toBeDisabled();
});
