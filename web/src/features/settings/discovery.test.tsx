import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { api } from "@/lib/api";
import { DiscoverySettings } from "./discovery";

it("reloads the latest server revision after a conflict", async () => {
  const edit = vi
    .spyOn(api, "edit")
    .mockRejectedValue(new Error("Configuration changed"));
  const get = vi.spyOn(api, "get").mockResolvedValue({
    revision: "latest",
    config: { naming: { mdns: { enabled: true, interfaces: ["eth2"] } } },
  });
  render(
    <DiscoverySettings
      settings={{ revision: "old", config: {} }}
      refresh={() => {}}
    />,
  );
  await userEvent.click(
    screen.getByRole("button", { name: "Save discovery settings" }),
  );
  await screen.findByRole("alert");
  await userEvent.click(
    screen.getByRole("button", { name: "Reload discovery settings" }),
  );
  expect(await screen.findByLabelText("LAN interfaces")).toHaveValue("eth2");
  expect(
    screen.getByLabelText("Discover device names with mDNS / Bonjour"),
  ).toBeChecked();
  edit.mockRestore();
  get.mockRestore();
});

it("saves enabled state and interface list with the captured revision", async () => {
  const edit = vi
    .spyOn(api, "edit")
    .mockResolvedValue({ revision: "revision-two" });
  render(
    <DiscoverySettings
      settings={{ revision: "revision-one", config: {} }}
      refresh={() => {}}
    />,
  );
  await userEvent.click(
    screen.getByLabelText("Discover device names with mDNS / Bonjour"),
  );
  await userEvent.type(screen.getByLabelText("LAN interfaces"), "eth0\nwlan0");
  await userEvent.click(
    screen.getByRole("button", { name: "Save discovery settings" }),
  );
  expect(edit).toHaveBeenCalledWith("revision-one", [
    { path: ["naming", "mdns", "enabled"], value: true },
    { path: ["naming", "mdns", "interfaces"], value: ["eth0", "wlan0"] },
  ]);
  expect(
    screen.queryByText("Discovery settings saved."),
  ).not.toBeInTheDocument();
  edit.mockRestore();
});
it("keeps the draft and revision on a conflict", async () => {
  const edit = vi
    .spyOn(api, "edit")
    .mockRejectedValue(new Error("Configuration changed"));
  const view = render(
    <DiscoverySettings
      settings={{ revision: "old", config: {} }}
      refresh={() => {}}
    />,
  );
  await userEvent.click(
    screen.getByLabelText("Discover device names with mDNS / Bonjour"),
  );
  await userEvent.click(
    screen.getByRole("button", { name: "Save discovery settings" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Configuration changed",
  );
  view.rerender(
    <DiscoverySettings
      settings={{ revision: "new", config: {} }}
      refresh={() => {}}
    />,
  );
  expect(
    screen.getByLabelText("Discover device names with mDNS / Bonjour"),
  ).toBeChecked();
  edit.mockRestore();
});
