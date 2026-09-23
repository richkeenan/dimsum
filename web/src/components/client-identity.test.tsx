import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ClientIdentity, DeviceDetails } from "./client-identity";

it("keeps the owner name while displaying discovered device evidence", () => {
  const device = {
    category: "tv" as const,
    reason: "Advertised television model",
    inferred: false,
    fresh: true,
    hostname: "android.local",
    evidence: [],
  };
  render(
    <>
      <ClientIdentity address="192.0.2.20" name="Owner TV" device={device} />
      <DeviceDetails device={device} />
    </>,
  );
  expect(screen.getByText("Owner TV")).toBeVisible();
  expect(screen.getByText("192.0.2.20")).toBeVisible();
  expect(screen.getByLabelText("TV")).toBeVisible();
  expect(screen.getByText("Advertised television model")).toBeVisible();
  expect(screen.getByText("android.local")).toBeVisible();
});
it("uses a generic device for older or unknown responses", () => {
  render(<ClientIdentity address="192.0.2.30" />);
  expect(screen.getByLabelText("Device")).toBeVisible();
  expect(screen.getByText("192.0.2.30")).toBeVisible();
});

it("marks DNS guesses and explains their observed domains", () => {
  const device = {
    category: "camera" as const,
    reason: "Queries to Ring firmware services",
    inferred: true,
    fresh: true,
    evidence: [],
    dns_guess: {
      rule: "ring",
      reason: "Queries to Ring firmware services",
      expires: "2026-09-23T10:00:00Z",
      domains: [
        {
          domain: "fw-eventstream.ring.com",
          first_seen: "2026-09-22T09:00:00Z",
          last_seen: "2026-09-22T10:00:00Z",
          queries: "3",
        },
      ],
    },
  };
  const view = render(
    <>
      <ClientIdentity address="192.0.2.20" name="Ring device" source="dns-guess" device={device} />
      <DeviceDetails device={device} />
    </>,
  );
  expect(screen.getByText("DNS guess")).toBeVisible();
  expect(screen.getByText("Ring device").parentElement).toHaveClass("inline-flex");
  expect(screen.getByText("DNS guess").parentElement).toBe(
    screen.getByText("Ring device").parentElement,
  );
  expect(screen.getByText("DNS query clues")).toBeVisible();
  expect(screen.getByText("fw-eventstream.ring.com")).toBeVisible();
  expect(screen.getByText(/3 queries/)).toBeVisible();
  expect(screen.queryByText("Local advertisements")).not.toBeInTheDocument();
  view.rerender(
    <ClientIdentity address="192.0.2.20" name="Owner name" source="override" device={device} />,
  );
  expect(screen.queryByText("DNS guess")).not.toBeInTheDocument();
  view.rerender(
    <ClientIdentity address="192.0.2.20" name="Retained camera name" device={device} />,
  );
  expect(screen.getByText("Retained camera name")).toBeVisible();
  expect(screen.queryByText("DNS guess")).not.toBeInTheDocument();
});

it("does not label hostname-based inference as a DNS query guess", () => {
  render(
    <ClientIdentity
      address="192.0.2.30"
      name="example-iphone.local"
      device={{
        category: "phone",
        reason: "Inferred from iPhone hostname",
        inferred: true,
        fresh: true,
        evidence: [],
      }}
    />,
  );
  expect(screen.queryByText("DNS guess")).not.toBeInTheDocument();
});
