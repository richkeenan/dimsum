import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, expect, it, vi } from "vitest";
import { api, APIError } from "@/lib/api";
import { BuiltinListEditor } from "./builtin-list";

const status = {
  saved_revision: "r1",
  active_revision: "r1",
  active_generation: "1",
  pending: false,
  restart_required: false,
  recovered: false,
  sources: [],
};
const baseline = {
  id: "work",
  revision: "r1",
  status,
  customized: false,
  entries: [{ domain: "analytics.example", origin: "builtin", removed: false }],
};
beforeEach(() => {
  vi.restoreAllMocks();
  vi.spyOn(api, "get").mockResolvedValue(baseline);
});
function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <BuiltinListEditor id="work" close={vi.fn()} />
    </QueryClientProvider>,
  );
}
it("removes a shipped exception and restores it using the latest read-back revision", async () => {
  const send = vi.spyOn(api, "send").mockImplementation(async (_path, _method, body) => {
    const action = (body as { action: string }).action;
    const revision = action === "remove" ? "r2" : "r3";
    const nextStatus = { ...status, saved_revision: revision, active_revision: revision };
    vi.mocked(api.get).mockResolvedValue({
      ...baseline,
      revision,
      status: nextStatus,
      customized: action === "remove",
      entries: [{ ...baseline.entries[0], removed: action === "remove" }],
    });
    return nextStatus;
  });
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "Remove analytics.example" }));
  expect(send).toHaveBeenLastCalledWith("builtin-lists/work", "PATCH", {
    revision: "r1",
    action: "remove",
    domain: "analytics.example",
  });
  fireEvent.click(await screen.findByRole("button", { name: "Restore analytics.example" }));
  await waitFor(() =>
    expect(send).toHaveBeenLastCalledWith("builtin-lists/work", "PATCH", {
      revision: "r2",
      action: "restore",
      domain: "analytics.example",
    }),
  );
});
it("keeps a failed addition available for retry without silently overwriting a conflict", async () => {
  const send = vi
    .spyOn(api, "send")
    .mockRejectedValue(new APIError(409, "conflict", "Configuration changed"));
  mount();
  await screen.findByText("analytics.example");
  fireEvent.change(screen.getByLabelText("Domain to allow"), {
    target: { value: "metrics.example" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add domain" }));
  await screen.findByText("Settings changed since you opened this form");
  expect(screen.getByLabelText("Domain to allow")).toHaveValue("metrics.example");
  expect(send).toHaveBeenCalledTimes(1);
});
it("resets customisations only after confirmation and displays activation failure", async () => {
  vi.mocked(api.get).mockResolvedValue({ ...baseline, customized: true });
  const send = vi.spyOn(api, "send").mockResolvedValue({
    ...status,
    saved_revision: "r2",
    pending: true,
    error: "Activation failed",
  });
  mount();
  await screen.findByText("analytics.example");
  fireEvent.click(await screen.findByRole("button", { name: "Reset to shipped defaults" }));
  expect(send).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Confirm reset" }));
  await screen.findByText(/Activation failed/);
  expect(send).toHaveBeenCalledExactlyOnceWith("builtin-lists/work", "PATCH", {
    revision: "r1",
    action: "reset",
  });
  await waitFor(() => expect(api.get).toHaveBeenCalledTimes(3), { timeout: 3500 });
  expect(screen.getByText(/Activation failed/)).toBeVisible();
});

it("follows an activation already pending when the editor opens", async () => {
  const pending = { ...status, saved_revision: "r2", pending: true };
  vi.mocked(api.get).mockResolvedValueOnce({ ...baseline, revision: "r2", status: pending });
  vi.mocked(api.get).mockResolvedValue({
    ...baseline,
    revision: "r2",
    status: { ...status, saved_revision: "r2", active_revision: "r2" },
  });
  mount();
  await screen.findByText("Applying changes…");
  await waitFor(() => expect(screen.queryByText("Applying changes…")).not.toBeInTheDocument(), {
    timeout: 3500,
  });
  expect(api.get).toHaveBeenCalledTimes(2);
});
