import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { api, APIError } from "@/lib/api";
import { UpstreamConnectionTest, UpstreamEditor } from "./upstreams";

afterEach(() => vi.restoreAllMocks());

it.each([
  [
    { healthy: true, responding: true, duration_us: "24000" },
    "Responded in 24.0 ms",
  ],
  [
    { healthy: false, responding: true, duration_us: "24000" },
    "Server responded with a DNS error",
  ],
  [
    { healthy: false, responding: false, duration_us: "1000000" },
    "No valid response",
  ],
])(
  "shows the completed connection result on the upstream row",
  async (result, message) => {
    const job = {
      id: "7",
      kind: "upstream-probe",
      state: "running" as const,
      created: "2026-01-01T00:00:00Z",
    };
    vi.spyOn(api, "send").mockResolvedValue(job);
    vi.spyOn(api, "get").mockResolvedValue({
      items: [{ ...job, state: "succeeded", result }],
    });
    render(<UpstreamConnectionTest address="192.0.2.53:53" />);
    fireEvent.click(screen.getByRole("button", { name: "Test connection" }));
    expect(await screen.findByRole("status")).toHaveTextContent(message);
    expect(
      screen.getByRole("button", { name: "Test connection" }),
    ).toBeEnabled();
  },
);

it("retains a custom draft and explains a server-side self-loop error", async () => {
  vi.spyOn(api, "send").mockRejectedValue(
    new APIError(
      400,
      "bad_request",
      "dns.upstreams[0]: endpoint points to DNS listener 127.0.0.1:53",
    ),
  );
  const saved = vi.fn();
  render(
    <UpstreamEditor
      original={{ address: "192.0.2.53:53", __index: 0 }}
      revision="revision"
      configured={[]}
      close={() => {}}
      saved={saved}
    />,
  );
  fireEvent.change(screen.getByLabelText("IP address"), {
    target: { value: "127.0.0.1" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Choose another DNS server",
  );
  expect(screen.getByLabelText("IP address")).toHaveValue("127.0.0.1");
  expect(saved).not.toHaveBeenCalled();
});

it("rejects invalid ports without a request and saves a corrected custom port", async () => {
  const send = vi.spyOn(api, "send").mockResolvedValue({});
  render(
    <UpstreamEditor
      original={{ address: "192.0.2.53:53", __index: 0 }}
      revision="revision"
      configured={[]}
      close={() => {}}
      saved={() => {}}
    />,
  );
  fireEvent.change(screen.getByLabelText("Port"), {
    target: { value: "65536" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("1 to 65535");
  expect(screen.getByLabelText("IP address")).not.toHaveAttribute(
    "aria-invalid",
    "true",
  );
  expect(screen.getByLabelText("Port")).toHaveAttribute("aria-invalid", "true");
  expect(send).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("Port"), {
    target: { value: "5353" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() =>
    expect(send).toHaveBeenCalledWith("upstreams", "PATCH", {
      revision: "revision",
      edits: [{ path: ["0"], value: "192.0.2.53:5353" }],
    }),
  );
});

it("keeps the interface scope when editing a link-local IPv6 server", async () => {
  const send = vi.spyOn(api, "send").mockResolvedValue({});
  render(
    <UpstreamEditor
      original={{ address: "[fe80::53%test0]:53", __index: 0 }}
      revision="revision"
      configured={[]}
      close={() => {}}
      saved={() => {}}
    />,
  );
  fireEvent.change(screen.getByLabelText("Port"), {
    target: { value: "5353" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() =>
    expect(send).toHaveBeenCalledWith("upstreams", "PATCH", {
      revision: "revision",
      edits: [{ path: ["0"], value: "[fe80::53%test0]:5353" }],
    }),
  );
});

it.each(["missing", "offline", "failed"])(
  "allows another connection test after a %s job result",
  async (state) => {
    const job = {
      id: "7",
      kind: "upstream-probe",
      state: "running",
      created: "2026-01-01T00:00:00Z",
    };
    vi.spyOn(api, "send").mockResolvedValue(job);
    const get = vi.spyOn(api, "get");
    if (state === "offline")
      get.mockRejectedValue(new Error("Connection lost"));
    else
      get.mockResolvedValue({
        items:
          state === "missing"
            ? []
            : [{ ...job, state: "failed", error: "test failed" }],
      });
    render(<UpstreamConnectionTest address="192.0.2.53:53" />);
    fireEvent.click(screen.getByRole("button", { name: "Test connection" }));
    expect(
      await screen.findByRole(state === "failed" ? "status" : "alert"),
    ).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Test connection" }),
    ).toBeEnabled();
  },
);

it("keeps an upstream draft after a stale revision is rejected", async () => {
  vi.spyOn(api, "send").mockRejectedValue(
    new APIError(409, "conflict", "revision conflict"),
  );
  const close = vi.fn();
  render(
    <UpstreamEditor
      original={{ address: "192.0.2.53:53", __index: 0 }}
      revision="old-revision"
      configured={[]}
      close={close}
      saved={() => {}}
    />,
  );
  fireEvent.change(screen.getByLabelText("Port"), {
    target: { value: "5353" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "haven't been saved",
  );
  expect(screen.getByLabelText("Port")).toHaveValue(5353);
  expect(close).not.toHaveBeenCalled();
});
