import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import Clients from "./index";

vi.mock("@tanstack/react-router", () => ({ useBlocker: () => {} }));
vi.mock("@/lib/hooks", () => ({
  useResource: (path: string) => ({
    loading: false,
    reload: async () => {},
    data:
      path === "profiles"
        ? { items: [{ id: "z", name: "Alpha profile" }] }
        : {
            status: { saved_revision: "r1" },
            items: [
              { policy_id: "offline", name: "Offline", address: "192.0.2.1" },
              { policy_id: "configured", name: "Zulu", profile: "z" },
            ],
            observed_available: true,
            observed: {
              items: [
                {
                  address: "192.0.2.2",
                  name: "Alpha",
                  count: "10",
                  blocked: "1",
                  last_seen: "2026-09-23T10:00:00Z",
                },
                {
                  address: "192.0.2.3",
                  client_id: "configured",
                  count: "6",
                  blocked: "2",
                  last_seen: "2026-09-23T10:01:00Z",
                },
                {
                  address: "2001:db8::3",
                  client_id: "configured",
                  count: "6",
                  blocked: "2",
                  last_seen: "2026-09-23T10:02:00Z",
                },
              ],
            },
          },
  }),
}));

function devices() {
  return screen
    .getAllByRole("row")
    .slice(1)
    .map((r) => within(r).getAllByRole("cell")[0].textContent);
}

it("sorts merged devices by total queries, displayed names, profiles and latest observation", () => {
  render(<Clients range="" onSelect={() => {}} onClientQueries={() => {}} />);
  expect(devices()).toEqual(["Zulu192.0.2.3", "Alpha192.0.2.2", "Offline192.0.2.1"]);
  expect(screen.getByRole("columnheader", { name: "Queries" })).toHaveAttribute(
    "aria-sort",
    "descending",
  );
  fireEvent.click(screen.getByRole("button", { name: "Device" }));
  expect(devices()).toEqual(["Alpha192.0.2.2", "Offline192.0.2.1", "Zulu192.0.2.3"]);
  fireEvent.change(screen.getByRole("textbox", { name: "Search devices" }), {
    target: { value: "192.0.2.3" },
  });
  expect(devices()).toEqual(["Zulu192.0.2.3"]);
  fireEvent.change(screen.getByRole("textbox", { name: "Search devices" }), {
    target: { value: "" },
  });
  expect(devices()[0]).toBe("Alpha192.0.2.2");
  fireEvent.click(screen.getByRole("button", { name: "Profile" }));
  expect(devices()[0]).toBe("Zulu192.0.2.3");
  fireEvent.click(screen.getByRole("button", { name: "Last seen" }));
  expect(devices()).toEqual(["Zulu192.0.2.3", "Alpha192.0.2.2", "Offline192.0.2.1"]);
  fireEvent.click(screen.getByRole("button", { name: "Last seen" }));
  expect(devices()).toEqual(["Alpha192.0.2.2", "Zulu192.0.2.3", "Offline192.0.2.1"]);
  fireEvent.click(screen.getByRole("button", { name: "Blocked" }));
  expect(devices()[0]).toBe("Zulu192.0.2.3");
});
