import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import type { DHCPStatusResponse } from "@/lib/api";
import { DHCPState } from "./index";

function status(): DHCPStatusResponse {
  return {
    status: {
      saved_revision: "one",
      active_revision: "one",
      active_generation: "25",
      pending: false,
      recovered: false,
      restart_required: false,
      sources: [],
    },
    runtime_available: true,
    dhcp: {
      state: "disabled",
      desired_enabled: false,
      applied_enabled: false,
      desired_generation: "25",
      applied_generation: "25",
      desired_interface: "eth0",
      desired_server_ip: "192.0.2.2",
      interface: "eth0",
      server_ip: "192.0.2.2",
      runtime: {
        generation: "25",
        storage: "closed",
        capacity: "1024",
        held: "0",
        pending: "0",
        clock_suspended: false,
        transport: {},
      },
    },
  };
}

it("reports DHCP off from the running service", () => {
  render(<DHCPState value={status()} />);
  expect(screen.getByRole("heading", { name: "DHCP is off" })).toBeVisible();
});

it.each([true, false])(
  "describes an unfinished enable/disable without claiming completion (%s)",
  (enable) => {
    const value = status();
    Object.assign(value.dhcp!, {
      desired_enabled: enable,
      applied_enabled: !enable,
      desired_generation: "26",
      pending_generation: "26",
    });
    render(<DHCPState value={value} />);
    expect(
      screen.getByRole("heading", {
        name: enable ? "Starting DHCP…" : "Stopping DHCP…",
      }),
    ).toBeVisible();
    expect(
      screen.queryByRole("heading", {
        name: enable ? "DHCP is on" : "DHCP is off",
      }),
    ).not.toBeInTheDocument();
  },
);

it("reports running service and useful network identity", () => {
  const value = status();
  Object.assign(value.dhcp!, {
    state: "running",
    desired_enabled: true,
    applied_enabled: true,
  });
  render(<DHCPState value={value} />);
  expect(screen.getByRole("heading", { name: "DHCP is on" })).toBeVisible();
  expect(screen.getByText(/192\.0\.2\.2/)).toBeVisible();
});

it("prioritizes a failed change over the updating state", () => {
  const value = status();
  value.status.pending = true;
  Object.assign(value.dhcp!, {
    state: "error",
    desired_enabled: true,
    last_error: "Interface eth0 is unavailable",
  });
  render(<DHCPState value={value} />);
  expect(
    screen.getByRole("heading", { name: "DHCP needs attention" }),
  ).toBeVisible();
  expect(screen.getByRole("alert")).toHaveTextContent(
    "Interface eth0 is unavailable",
  );
});

it("does not misrepresent unavailable runtime information as off", () => {
  const value = status();
  value.runtime_available = false;
  value.dhcp = null;
  render(<DHCPState value={value} />);
  expect(
    screen.getByRole("heading", { name: "DHCP status unavailable" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("heading", { name: "DHCP is off" }),
  ).not.toBeInTheDocument();
});
