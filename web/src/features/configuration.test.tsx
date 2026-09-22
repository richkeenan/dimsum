import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import Configuration from "./configuration";
import SettingsView, { PasswordForm, Revision } from "./settings";
import Jobs from "./settings/jobs";
import Diagnostics from "./diagnostics";
import { api, APIError } from "@/lib/api";

const resources = vi.hoisted(() => ({
  values: {} as Record<
    string,
    { data?: unknown; loading: boolean; error?: Error }
  >,
}));
vi.mock("@/lib/hooks", () => ({
  useResource: (path: string) => resources.values[path] ?? { loading: false },
}));

beforeEach(() => {
  vi.restoreAllMocks();
  resources.values = {
    settings: {
      loading: false,
      data: {
        revision: "original-revision",
        config: {
          cache: { bytes: 8388608, stale_mode: "off" },
          dns: { listen: ["127.0.0.1:5353"] },
        },
      },
    },
    lists: {
      loading: false,
      data: { revision: "original-revision", items: [] },
    },
    records: {
      loading: false,
      data: { revision: "original-revision", items: [] },
    },
    catalog: {
      loading: false,
      data: {
        items: [
          {
            id: "recommended",
            label: "Recommended domains",
            description: "A balanced list",
            url: "https://example.com/domains",
            dialect: "dns-adblock",
            domain_kind: "suffix",
            available: true,
            default_enabled: true,
          },
        ],
      },
    },
  };
});

it("waits for an editable revision before opening a form", () => {
  resources.values.records = { loading: true };
  const view = render(<Configuration kind="records" range="" />);
  expect(screen.getByRole("button", { name: "Add record" })).toBeDisabled();
  resources.values.records = {
    loading: false,
    data: { revision: "ready", items: [] },
  };
  view.rerender(<Configuration kind="records" range="" />);
  expect(screen.getByRole("button", { name: "Add record" })).toBeEnabled();
});

it("uses catalog values and generates an ID without requiring a manual one", async () => {
  const send = vi.spyOn(api, "send").mockResolvedValue({});
  render(<Configuration kind="lists" range="" />);
  fireEvent.click(screen.getByRole("button", { name: "Add list" }));
  const dialog = screen.getByRole("dialog");
  expect(within(dialog).queryByText("Source ID")).not.toBeInTheDocument();
  fireEvent.change(within(dialog).getByLabelText(/Start with a list/), {
    target: { value: "recommended" },
  });
  expect(within(dialog).getByRole("combobox", { name: "Format" })).toHaveValue(
    "dns-adblock",
  );
  fireEvent.click(within(dialog).getByRole("button", { name: "Add list" }));
  await waitFor(() =>
    expect(send).toHaveBeenCalledWith("lists", "POST", {
      revision: "original-revision",
      item: {
        id: expect.stringMatching(/^list-/),
        url: "https://example.com/domains",
        dialect: "dns-adblock",
        domain_kind: "suffix",
        enabled: true,
      },
    }),
  );
});

it("offers only supported local records with a five-minute lifetime and reverse lookups off", () => {
  render(<Configuration kind="records" range="" />);
  fireEvent.click(screen.getByRole("button", { name: "Add record" }));
  const dialog = screen.getByRole("dialog");
  expect(
    within(dialog).queryByRole("option", { name: "TXT" }),
  ).not.toBeInTheDocument();
  expect(within(dialog).getByLabelText("Cache lifetime (seconds)")).toHaveValue(
    300,
  );
  expect(
    within(dialog).getByRole("combobox", {
      name: "Create a reverse lookup too",
    }),
  ).toHaveValue("false");
});

it("offers a canonical dashboard hostname after saving and shows the link only after approval", async () => {
  const send = vi
    .spyOn(api, "send")
    .mockResolvedValueOnce({
      saved_revision: "record-saved",
      dashboard_hosts: [
        {
          name: "xn--bcher-kva.test",
          host: "xn--bcher-kva.test:18080",
          url: "http://xn--bcher-kva.test:18080/",
        },
      ],
    })
    .mockResolvedValueOnce({ saved_revision: "host-saved" });
  render(<Configuration kind="records" range="" />);
  fireEvent.click(screen.getByRole("button", { name: "Add record" }));
  fireEvent.change(screen.getByLabelText("DNS name"), {
    target: { value: "bücher.test" },
  });
  fireEvent.change(screen.getByLabelText(/Address or target/), {
    target: { value: "192.0.2.10" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  const confirm = await screen.findByRole("dialog", {
    name: /Use xn--bcher-kva.test for the dashboard/,
  });
  expect(
    screen.queryByRole("link", { name: "http://xn--bcher-kva.test:18080/" }),
  ).not.toBeInTheDocument();
  fireEvent.click(
    within(confirm).getByRole("button", { name: "Add to accepted hosts" }),
  );
  expect(
    await screen.findByRole("link", {
      name: "http://xn--bcher-kva.test:18080/",
    }),
  ).toHaveAttribute("href", "http://xn--bcher-kva.test:18080/");
  expect(send).toHaveBeenLastCalledWith("records", "PATCH", {
    revision: "record-saved",
    accept_admin_host: "xn--bcher-kva.test",
  });
});

it("keeps the DNS record when dashboard approval is declined", async () => {
  const send = vi.spyOn(api, "send").mockResolvedValue({
    saved_revision: "record-saved",
    dashboard_hosts: [
      {
        name: "dimsum.test",
        host: "dimsum.test:18080",
        url: "http://dimsum.test:18080/",
      },
    ],
  });
  render(<Configuration kind="records" range="" />);
  fireEvent.click(screen.getByRole("button", { name: "Add record" }));
  fireEvent.change(screen.getByLabelText("DNS name"), {
    target: { value: "dimsum.test" },
  });
  fireEvent.change(screen.getByLabelText(/Address or target/), {
    target: { value: "192.0.2.10" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  fireEvent.click(await screen.findByRole("button", { name: "Not now" }));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(screen.getByRole("status")).toHaveTextContent("Changes saved");
  expect(send).toHaveBeenCalledTimes(1);
});

it("preserves an existing ID and the editing revision while background data changes", async () => {
  resources.values.lists.data = {
    revision: "old",
    items: [
      {
        id: "keep-this-id",
        url: "https://example.com/old",
        dialect: "domains",
        domain_kind: "exact",
        enabled: true,
      },
    ],
  };
  const send = vi.spyOn(api, "send").mockResolvedValue({});
  const view = render(<Configuration kind="lists" range="" />);
  fireEvent.click(screen.getByRole("button", { name: "Edit" }));
  fireEvent.change(screen.getByRole("textbox", { name: /List URL/ }), {
    target: { value: "https://example.com/new" },
  });
  resources.values.lists.data = { revision: "new", items: [] };
  view.rerender(<Configuration kind="lists" range="" />);
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() =>
    expect(send).toHaveBeenCalledWith("lists", "PATCH", {
      revision: "old",
      edits: [{ path: ["0", "url"], value: "https://example.com/new" }],
    }),
  );
});

it("patches only changed settings and retains the draft on a revision conflict", async () => {
  const edit = vi
    .spyOn(api, "edit")
    .mockRejectedValue(new APIError(409, "conflict", "changed"));
  const view = render(<SettingsView />);
  fireEvent.change(screen.getByLabelText("Memory budget (bytes)"), {
    target: { value: "16777216" },
  });
  resources.values.settings = {
    loading: false,
    data: { revision: "polled-revision", config: { cache: { bytes: 1024 } } },
  };
  view.rerender(<SettingsView />);
  expect(screen.getByLabelText("Memory budget (bytes)")).toHaveValue(16777216);
  fireEvent.click(screen.getByRole("button", { name: "Save settings" }));
  await waitFor(() =>
    expect(edit).toHaveBeenCalledWith("original-revision", [
      { path: ["cache", "bytes"], value: 16777216 },
    ]),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(/changed/i);
  expect(screen.getByLabelText("Memory budget (bytes)")).toHaveValue(16777216);
});

it("hides ordinary revision identifiers and only surfaces exceptional activation state", () => {
  const view = render(
    <Revision value={{ revision: "private-hash", active_generation: "42" }} />,
  );
  expect(view.container).toBeEmptyDOMElement();
  view.rerender(
    <Revision value={{ revision: "private-hash", pending: true }} />,
  );
  expect(screen.getByRole("status")).toHaveTextContent(
    "Applying saved changes",
  );
  expect(screen.queryByText("private-hash")).not.toBeInTheDocument();
});

it("changes the password and expires the session only after a successful response", async () => {
  const send = vi.spyOn(api, "send").mockResolvedValue({});
  const expired = vi.fn();
  window.addEventListener("session-expired", expired);
  sessionStorage.setItem("dimsum-csrf", "old-token");
  render(<PasswordForm />);
  fireEvent.change(screen.getByLabelText("New password"), {
    target: { value: "new-secret" },
  });
  fireEvent.change(screen.getByLabelText("Confirm new password"), {
    target: { value: "different" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Change password" }));
  expect(send).not.toHaveBeenCalled();
  expect(expired).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("Confirm new password"), {
    target: { value: "new-secret" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Change password" }));
  await waitFor(() => expect(expired).toHaveBeenCalledOnce());
  expect(send).toHaveBeenCalledWith("password", "POST", {
    password: "new-secret",
  });
  expect(sessionStorage.getItem("dimsum-csrf")).toBeNull();
  window.removeEventListener("session-expired", expired);
});

it("keeps one row per device and uses its configured friendly name", () => {
  resources.values["clients?&limit=200"] = {
    loading: false,
    data: {
      revision: "ready",
      observed_available: true,
      items: [{ address: "192.0.2.1", name: "Kitchen" }],
      observed: {
        items: [
          { address: "192.0.2.1", count: "12" },
          { address: "192.0.2.2", count: "2" },
        ],
      },
    },
  };
  render(<Configuration kind="clients" range="" />);
  expect(screen.getAllByText("192.0.2.1")).toHaveLength(1);
  expect(screen.getAllByText("192.0.2.2")).toHaveLength(1);
  expect(screen.getByText("Kitchen")).toBeInTheDocument();
  expect(screen.getAllByRole("table")).toHaveLength(1);
});

it("opens client queries from the name and discovery details from a separate action", () => {
  resources.values["clients?&limit=200"] = {
    loading: false,
    data: {
      revision: "ready",
      observed_available: true,
      items: [],
      observed: {
        items: [{ address: "192.0.2.20", name: "Example television" }],
      },
    },
  };
  const queries = vi.fn();
  render(<Configuration kind="clients" range="" onClientQueries={queries} />);
  fireEvent.click(
    screen.getByRole("button", { name: "View queries for Example television" }),
  );
  expect(queries).toHaveBeenCalledWith("192.0.2.20");
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", {
      name: "Device details for Example television",
    }),
  );
  expect(screen.getByRole("dialog")).toHaveTextContent("192.0.2.20");
  expect(queries).toHaveBeenCalledTimes(1);
});

it("keeps device details open and current across client refreshes and reordering", () => {
  const client = {
    address: "192.0.2.20",
    name: "Example television",
    count: "12",
    name_source: "dns-sd",
    device: {
      category: "tv",
      reason: "Advertised model",
      model: "Example Model",
      fresh: true,
      inferred: false,
      evidence: [],
    },
  };
  const data = {
    revision: "ready",
    observed_available: true,
    items: [],
    observed: { items: [client, { address: "192.0.2.21", count: "2" }] },
  };
  resources.values["clients?&limit=200"] = { loading: false, data };
  const view = render(<Configuration kind="clients" range="" />);
  fireEvent.click(
    screen.getByRole("button", {
      name: "Device details for Example television",
    }),
  );
  expect(screen.getByRole("dialog")).toHaveTextContent("Example Model");

  resources.values["clients?&limit=200"] = {
    loading: false,
    data: {
      ...data,
      observed: {
        items: [
          { address: "192.0.2.21", count: "24" },
          {
            ...client,
            name: "Living room television",
            count: "13",
            device: { ...client.device, model: "Updated Model" },
          },
        ],
      },
    },
  };
  view.rerender(<Configuration kind="clients" range="" />);
  const dialog = screen.getByRole("dialog");
  expect(dialog).toHaveTextContent("Living room television");
  expect(dialog).toHaveTextContent("Updated Model");
  fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});

it("explains a blocked rule test without displaying raw JSON by default", async () => {
  resources.values.rules = {
    loading: false,
    data: { revision: "ready", items: [] },
  };
  vi.spyOn(api, "send").mockResolvedValue({
    normalized: "ads.example.com",
    decision: { result: "block", rule_id: "internal-rule" },
  });
  render(<Configuration kind="rules" range="" />);
  fireEvent.change(screen.getByLabelText("Domain"), {
    target: { value: "ads.example.com" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Test rule" }));
  expect(await screen.findByText("Blocked")).toBeVisible();
  expect(screen.getByText(/internal-rule/)).not.toBeVisible();
});

it("keeps the current session if changing the password fails", async () => {
  vi.spyOn(api, "send").mockRejectedValue(
    new Error("Password could not be saved"),
  );
  sessionStorage.setItem("dimsum-csrf", "keep-token");
  const expired = vi.fn();
  window.addEventListener("session-expired", expired);
  render(<PasswordForm />);
  fireEvent.change(screen.getByLabelText("New password"), {
    target: { value: "new-secret" },
  });
  fireEvent.change(screen.getByLabelText("Confirm new password"), {
    target: { value: "new-secret" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Change password" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Password could not be saved",
  );
  expect(sessionStorage.getItem("dimsum-csrf")).toBe("keep-token");
  expect(expired).not.toHaveBeenCalled();
  window.removeEventListener("session-expired", expired);
});

it("provides a direct backup action and requires an archive before restoring", async () => {
  resources.values.jobs = { loading: false, data: { items: [] } };
  const send = vi
    .spyOn(api, "send")
    .mockResolvedValue({ id: "backup-job", kind: "backup", state: "running" });
  render(<Jobs />);
  expect(
    screen.getByRole("button", { name: "Validate and restore" }),
  ).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Create backup" }));
  await waitFor(() =>
    expect(send).toHaveBeenCalledWith("jobs", "POST", {
      kind: "backup",
      input: {},
    }),
  );
  expect(screen.getByRole("status")).toHaveTextContent("Operation in progress");
});

it("summarises diagnostic health and keeps raw measurements collapsed", () => {
  resources.values.diagnostics = {
    loading: false,
    data: {
      dns_ready: true,
      boot_id: "raw-boot-id",
      storage: { available: false, error: "Disk unavailable" },
      upstreams: [{ Endpoint: "192.0.2.53:53", State: "open" }],
    },
  };
  render(<Diagnostics />);
  expect(screen.getByText("Ready to answer queries")).toBeVisible();
  expect(screen.getByText("Storage unavailable")).toBeVisible();
  expect(screen.getByText(/Waiting before retry/)).toBeVisible();
  expect(screen.getByText("raw-boot-id")).not.toBeVisible();
});
