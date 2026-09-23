import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { api, type DHCPConfigResponse, type DHCPSettings } from "@/lib/api";
import { DHCPForm } from "./index";

const status = {
  saved_revision: "one",
  active_revision: "one",
  active_generation: "1",
  pending: false,
  recovered: false,
  restart_required: false,
  sources: [],
};
const empty: DHCPSettings = {
  enabled: false,
  interface: "",
  server_ip: "",
  gateway: "",
  subnet: "",
  range_start: "",
  range_end: "",
  lease_seconds: 0,
  local_domain: "",
  max_leases: 0,
  reservations: [],
};
const suggested: DHCPSettings = {
  ...empty,
  interface: "eth0",
  server_ip: "192.0.2.2",
  gateway: "192.0.2.1",
  subnet: "192.0.2.0/24",
  range_start: "192.0.2.128",
  range_end: "192.0.2.227",
  lease_seconds: 86400,
  local_domain: "home.arpa",
};
function fixture(): DHCPConfigResponse {
  return {
    status,
    config: empty,
    setup: {
      config: suggested,
      suggested: [
        "interface",
        "server_ip",
        "subnet",
        "gateway",
        "range_start",
        "range_end",
        "lease_seconds",
        "local_domain",
      ],
      fixed_address: "yes",
      message: "",
    },
  };
}
function form(value = fixture()) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  client.setQueryData(["api", "dhcp", "dhcp"], value);
  return render(
    <QueryClientProvider client={client}>
      <DHCPForm applied={{ status, dhcp: null, runtime_available: false }} refresh={() => {}} />
    </QueryClientProvider>,
  );
}
afterEach(() => vi.restoreAllMocks());

it("starts with real detected values and enables without entering any fields", async () => {
  const send = vi.spyOn(api, "send").mockResolvedValue(status);
  vi.spyOn(api, "get").mockResolvedValue({
    status,
    config: { ...suggested, enabled: true },
  });
  form();
  expect(screen.getByLabelText("Server IP address")).toHaveValue("192.0.2.2");
  expect(screen.getByLabelText("Server IP address")).not.toBeVisible();
  expect(screen.getByText("192.0.2.128 – 192.0.2.227")).toBeVisible();
  expect(send).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("switch", { name: "Enable DHCP" }));
  await userEvent.click(screen.getByRole("button", { name: "Save and enable" }));
  await waitFor(() =>
    expect(send).toHaveBeenCalledWith("dhcp", "PATCH", {
      revision: "one",
      edits: expect.arrayContaining([
        { path: ["enabled"], value: true },
        { path: ["server_ip"], value: "192.0.2.2" },
        { path: ["lease_seconds"], value: 86400 },
        { path: ["local_domain"], value: "home.arpa" },
      ]),
    }),
  );
});

it("lets users edit suggestions and save them while keeping DHCP off", async () => {
  const send = vi.spyOn(api, "send").mockRejectedValue(new Error("Write unavailable"));
  form();
  await userEvent.click(screen.getByText("Edit settings", { exact: true }));
  await userEvent.clear(screen.getByLabelText("First IP address"));
  await userEvent.type(screen.getByLabelText("First IP address"), "192.0.2.140");
  await userEvent.selectOptions(screen.getByLabelText("Lease duration"), "3600");
  await userEvent.click(screen.getByRole("button", { name: "Save settings" }));
  const edits = send.mock.calls[0][2] as {
    edits: { path: string[]; value: unknown }[];
  };
  expect(edits.edits).toContainEqual({
    path: ["range_start"],
    value: "192.0.2.140",
  });
  expect(edits.edits).toContainEqual({ path: ["lease_seconds"], value: 3600 });
  expect(edits.edits).not.toContainEqual({ path: ["enabled"], value: true });
});

it("opens missing fields when detection cannot choose a network", () => {
  const value = fixture();
  value.setup!.config = {
    ...empty,
    lease_seconds: 86400,
    local_domain: "home.arpa",
  };
  value.setup!.message = "Couldn’t choose one network automatically.";
  value.setup!.fixed_address = "unknown";
  form(value);
  expect(screen.getByLabelText("Network interface")).toBeVisible();
  expect(screen.getByLabelText("Network interface")).toHaveValue("");
  expect(screen.getByText("Couldn’t choose one network automatically.")).toBeVisible();
});

it("explains a dynamic server address without pretending it is fixed", () => {
  const value = fixture();
  value.setup!.fixed_address = "no";
  form(value);
  expect(screen.getByText(/server gets its address automatically/)).toBeVisible();
});

it("keeps existing saved settings authoritative", async () => {
  const value = fixture();
  value.config = {
    ...suggested,
    lease_seconds: 7200,
    range_start: "192.0.2.30",
    range_end: "192.0.2.60",
  };
  value.setup!.config = { ...value.config };
  value.setup!.suggested = [];
  form(value);
  await userEvent.click(screen.getByText("Edit settings", { exact: true }));
  expect(screen.getByLabelText("First IP address")).toHaveValue("192.0.2.30");
  expect(screen.getByLabelText("Custom duration (seconds)")).toHaveValue(7200);
  expect(screen.getByRole("button", { name: "Save settings" })).toBeDisabled();
});
