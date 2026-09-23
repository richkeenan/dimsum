import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ProfileAssignment, ProfileMap } from "./assignment";

it("sorts names naturally and filters immediately while retaining empty drop targets", () => {
  const rows = ["Tablet 10", "alpha", "Tablet 2"].map((name) => ({
    key: name,
    configured: { id: name, policy_id: name, name },
    observed: [],
  }));
  render(
    <ProfileMap
      rows={rows}
      profiles={[{ id: "kids", name: "Kids" }]}
      reload={vi.fn()}
      onEdit={vi.fn()}
    />,
  );
  expect(screen.getAllByRole("combobox").map((el) => el.getAttribute("aria-label"))).toEqual([
    "Profile for alpha",
    "Profile for Tablet 2",
    "Profile for Tablet 10",
  ]);
  fireEvent.change(screen.getByRole("searchbox", { name: "Find device" }), {
    target: { value: "TABLET 2" },
  });
  expect(screen.getAllByRole("combobox")).toHaveLength(1);
  expect(screen.getByRole("region", { name: "Kids devices" })).toBeInTheDocument();
  expect(screen.getByText("1 of 3 devices")).toBeInTheDocument();
});
import { api } from "@/lib/api";

afterEach(() => vi.restoreAllMocks());

it("keeps a device in its group and reports a failed drop", async () => {
  const configured = { id: "tablet", policy_id: "tablet", name: "Tablet" };
  vi.spyOn(api, "get").mockResolvedValue({
    status: { saved_revision: "latest" },
    items: [configured],
  });
  vi.spyOn(api, "send").mockRejectedValue(new Error("Settings changed. Refresh and try again."));
  render(
    <ProfileMap
      rows={[{ key: "tablet", configured, observed: [] }]}
      profiles={[{ id: "kids", name: "Kids" }]}
      reload={vi.fn()}
      onEdit={vi.fn()}
    />,
  );
  const dataTransfer = { setData: vi.fn(), effectAllowed: "", dropEffect: "" };
  fireEvent.dragStart(screen.getByTitle("Drag Tablet to a profile"), {
    dataTransfer,
  });
  fireEvent.dragOver(screen.getByRole("region", { name: "Kids devices" }), {
    dataTransfer,
  });
  fireEvent.drop(screen.getByRole("region", { name: "Kids devices" }), {
    dataTransfer,
  });
  expect(await screen.findByRole("alert")).toHaveTextContent("Settings changed");
  expect(screen.getByRole("combobox")).toHaveValue("");
});

it("assigns an observed device using its current lease MAC in one transaction", async () => {
  const observation = {
    address: "192.0.2.10",
    name: "Tablet",
    authoritative_mac: "02:00:00:00:00:10",
  };
  vi.spyOn(api, "get").mockResolvedValue({
    status: { saved_revision: "latest" },
    items: [],
    observed: { items: [observation] },
  });
  const send = vi.spyOn(api, "send").mockResolvedValue({
    saved_revision: "saved",
    active_revision: "saved",
    sources: [],
  });
  const reload = vi.fn();
  render(
    <ProfileAssignment
      row={{ key: "observed:192.0.2.10", observed: [observation] as any }}
      profiles={[{ id: "kids", name: "Kids" }]}
      reload={reload}
    />,
  );
  fireEvent.change(screen.getByRole("combobox"), { target: { value: "kids" } });
  await waitFor(() =>
    expect(send).toHaveBeenCalledWith(
      "client-policy",
      "PATCH",
      expect.objectContaining({
        revision: "latest",
        scope: "client",
        create: true,
        lease_address: "192.0.2.10",
        profile: "kids",
        name: "Tablet",
      }),
    ),
  );
  expect(send.mock.calls[0][2]).not.toHaveProperty("selectors");
  await waitFor(() => expect(reload).toHaveBeenCalled());
});

it("changes an existing device profile without replacing its other settings", async () => {
  const configured = {
    id: "tablet",
    policy_id: "tablet",
    profile: "kids",
    name: "Tablet",
  };
  vi.spyOn(api, "get").mockResolvedValue({
    status: { saved_revision: "latest" },
    items: [configured],
  });
  const send = vi.spyOn(api, "send").mockResolvedValue({
    saved_revision: "saved",
    active_revision: "saved",
    sources: [],
  });
  render(
    <ProfileAssignment
      row={{ key: "tablet", configured, observed: [] }}
      profiles={[{ id: "kids", name: "Kids" }]}
      reload={vi.fn()}
    />,
  );
  fireEvent.change(screen.getByRole("combobox"), { target: { value: "" } });
  await waitFor(() =>
    expect(send).toHaveBeenCalledWith("client-policy", "PATCH", {
      revision: "latest",
      scope: "client",
      id: "tablet",
      profile: "",
    }),
  );
});
