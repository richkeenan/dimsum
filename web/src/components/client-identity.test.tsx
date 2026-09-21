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
