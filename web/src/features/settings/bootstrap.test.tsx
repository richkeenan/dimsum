import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { api, APIError } from "@/lib/api";
import { BootstrapSettings } from "./bootstrap";

afterEach(() => vi.restoreAllMocks());

it("uses automatic defaults and saves a custom bootstrap list with the draft revision", async () => {
  const edit = vi.spyOn(api, "edit").mockResolvedValue({ revision: "r3" });
  const { rerender } = render(
    <BootstrapSettings
      settings={{ revision: "r1", config: {} }}
      refresh={() => {}}
    />,
  );
  fireEvent.click(screen.getByText("Advanced: bootstrap DNS"));
  expect(screen.getByLabelText("Bootstrap DNS servers")).toHaveValue(
    "1.1.1.1:53\n9.9.9.9:53",
  );
  fireEvent.change(screen.getByLabelText("Bootstrap DNS servers"), {
    target: { value: "192.0.2.53:53\n[2001:db8::53]:5353" },
  });
  rerender(
    <BootstrapSettings
      settings={{ revision: "r2", config: {} }}
      refresh={() => {}}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Save bootstrap DNS" }));
  await waitFor(() =>
    expect(edit).toHaveBeenCalledWith("r1", [
      {
        path: ["dns", "bootstrap_dns"],
        value: ["192.0.2.53:53", "[2001:db8::53]:5353"],
      },
    ]),
  );
});

it("resets bootstrap to the explicit default pair through settings editing", async () => {
  const edit = vi.spyOn(api, "edit").mockResolvedValue({ revision: "r2" });
  render(
    <BootstrapSettings
      settings={{
        revision: "r1",
        config: { dns: { bootstrap_dns: ["192.0.2.53:53"] } },
      }}
      refresh={() => {}}
    />,
  );
  fireEvent.click(screen.getByText("Advanced: bootstrap DNS"));
  fireEvent.click(
    screen.getByRole("button", { name: "Use automatic defaults" }),
  );
  expect(screen.getByLabelText("Bootstrap DNS servers")).toHaveValue(
    "1.1.1.1:53\n9.9.9.9:53",
  );
  fireEvent.click(screen.getByRole("button", { name: "Save bootstrap DNS" }));
  await waitFor(() =>
    expect(edit).toHaveBeenCalledWith("r1", [
      { path: ["dns", "bootstrap_dns"], value: ["1.1.1.1:53", "9.9.9.9:53"] },
    ]),
  );
});

it("rejects an empty bootstrap override and preserves a rejected draft", async () => {
  const edit = vi
    .spyOn(api, "edit")
    .mockRejectedValue(new APIError(409, "conflict", "revision conflict"));
  render(
    <BootstrapSettings settings={{ revision: "r1" }} refresh={() => {}} />,
  );
  fireEvent.click(screen.getByText("Advanced: bootstrap DNS"));
  fireEvent.change(screen.getByLabelText("Bootstrap DNS servers"), {
    target: { value: "" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save bootstrap DNS" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(/1 to 16/);
  expect(edit).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("Bootstrap DNS servers"), {
    target: { value: "192.0.2.53:53" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save bootstrap DNS" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    /haven't been saved/,
  );
  expect(screen.getByLabelText("Bootstrap DNS servers")).toHaveValue(
    "192.0.2.53:53",
  );
});
