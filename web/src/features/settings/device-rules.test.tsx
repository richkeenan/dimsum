import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { DeviceRules } from "./device-rules";

afterEach(() => vi.unstubAllGlobals());
const status = {
  saved_revision: "r1",
  active_revision: "r1",
  active_generation: "1",
  pending: false,
  recovered: false,
  restart_required: false,
  sources: [],
};
const rule = {
  id: "washer",
  name: "Example washer",
  category: "appliance",
  domains: ["washer.example"],
};
function setup(conflict = false) {
  const writes: any[] = [];
  let revision = "r1";
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url, init?: RequestInit) => {
      if (init?.method === "PATCH") {
        writes.push(JSON.parse(String(init.body)));
        if (conflict)
          return Response.json(
            {
              error: {
                code: "revision_conflict",
                message: "Configuration changed. Reload before saving.",
              },
            },
            { status: 409 },
          );
        revision = "r2";
        return Response.json({ ...status, saved_revision: revision, active_revision: revision });
      }
      return Response.json({
        revision,
        status: { ...status, saved_revision: revision, active_revision: revision },
        customized: true,
        entries: [{ rule, builtin: rule, origin: "modified", enabled: true, available: true }],
      });
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <DeviceRules />
    </QueryClientProvider>,
  );
  return writes;
}
it("edits domains and icon against the opened revision", async () => {
  const writes = setup();
  fireEvent.click(await screen.findByRole("button", { name: "Edit Example washer" }));
  fireEvent.change(screen.getByLabelText("DNS domains"), {
    target: { value: "washer.example\nevents.washer.example" },
  });
  fireEvent.change(screen.getByLabelText("Icon"), { target: { value: "washing-machine" } });
  fireEvent.click(screen.getByRole("button", { name: "Save rule" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toMatchObject({
    revision: "r1",
    action: "save",
    id: "washer",
    rule: { icon: "washing-machine", domains: ["washer.example", "events.washer.example"] },
  });
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
});
it("retains an edited rule when the server rejects its revision", async () => {
  const writes = setup(true);
  fireEvent.click(await screen.findByRole("button", { name: "Edit Example washer" }));
  fireEvent.change(screen.getByLabelText("Rule name"), { target: { value: "My washer" } });
  fireEvent.click(screen.getByRole("button", { name: "Save rule" }));
  expect(await screen.findByText("Settings changed since you opened this form")).toBeVisible();
  expect(screen.getByLabelText("Rule name")).toHaveValue("My washer");
  expect(writes[0].revision).toBe("r1");
});
it("resets all customisations only after confirmation", async () => {
  const writes = setup();
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  fireEvent.click(await screen.findByRole("button", { name: "Reset all rules" }));
  expect(writes).toHaveLength(0);
  confirm.mockReturnValue(true);
  fireEvent.click(screen.getByRole("button", { name: "Reset all rules" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toEqual({ revision: "r1", action: "reset" });
  confirm.mockRestore();
});
