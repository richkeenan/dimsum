import { test, expect } from "../../web/e2e";
import { connectDHCPAgent } from "../../web/scripts/mcp-client-check.mjs";

test.skip(
  !process.env.DIMSUM_E2E_CONFIG,
  "Requires the isolated Go API harness",
);
test("DHCP UI and MCP share revisions, reservations and disabled lease inspection", async ({
  page,
}, testInfo) => {
  await page.goto("/settings");
  await page
    .getByLabel("Admin password")
    .fill(process.env.DIMSUM_E2E_PASSWORD!);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page
    .getByLabel("Token name", { exact: true })
    .fill("DHCP parity fixture");
  await page.getByRole("button", { name: "Create token", exact: true }).click();
  const token = await page
    .getByLabel("New token", { exact: true })
    .inputValue();
  const agent = await connectDHCPAgent(new URL("/mcp", page.url()).href, token);
  const call = async (name: string, args = {}) => {
    const result = await agent.callTool({ name, arguments: args });
    expect(result.isError).not.toBe(true);
    return result.structuredContent as any;
  };
  try {
    await page.getByRole("button", { name: "Done", exact: true }).click();
    await page.getByRole("link", { name: "DHCP", exact: true }).click();
    await expect(page.getByLabel("Enable DHCP")).not.toBeChecked();
    const inspected = await call("get_dhcp");
    if (!inspected.availability.supported) {
      await expect(page.getByRole("heading", { name: "DHCP is unavailable" })).toBeVisible();
      await expect(page.getByRole("region", { name: "DHCP status" }).getByText(inspected.availability.reason)).toBeVisible();
      await expect(page.getByLabel("Enable DHCP")).toBeDisabled();
      await expect(page.getByLabel("Subnet")).toBeDisabled();
      await expect(page.getByRole("button", { name: "Add reservation" })).toBeDisabled();
      await page.getByText("Troubleshooting", { exact: true }).click();
      await expect(page.getByRole("button", { name: "Check setup" })).toBeDisabled();
      expect((await call("get_dhcp")).status.saved_revision).toBe(inspected.status.saved_revision);
      return;
    }
    if (!(await page.getByLabel("Subnet").isVisible())) {
      await page.getByText("Edit settings", { exact: true }).click();
    }
    await page.getByLabel("Subnet").fill("192.0.2.0/24");
    // Replace host-derived suggestions as a coherent synthetic network.
    await page.getByLabel("Network interface").fill("fixture0");
    await page.getByLabel("Server IP address", { exact: true }).fill("192.0.2.2");
    await page.getByLabel("Router IP address", { exact: true }).fill("192.0.2.1");
    await page.getByLabel("First IP address").fill("192.0.2.100");
    await page.getByLabel("Last IP address").fill("192.0.2.199");
    await page.getByLabel("Local domain", { exact: true }).fill("home.arpa");
    await page.getByRole("button", { name: "Save settings" }).click();
    await expect(page.getByText(/Settings saved/)).toBeVisible();
    const saved = await call("get_dhcp");
    expect(saved.config.enabled).toBe(false);
    expect(saved.config.subnet).toBe("192.0.2.0/24");
    // Hold a UI draft across an agent mutation; it must not rebase silently.
    await page.getByLabel("Network interface").fill("draft0");
    await call("update_dhcp", {
      body: {
        revision: saved.status.saved_revision,
        edits: [{ path: ["interface"], value: "fixture0" }],
      },
    });
    await page.getByRole("button", { name: "Save settings" }).click();
    await expect(page.getByRole("alert")).toContainText("Settings changed");
    await expect(page.getByLabel("Network interface")).toHaveValue("draft0");
    await page.getByRole("button", { name: "Reload settings" }).click();
    await expect(page.getByLabel("Network interface")).toHaveValue("fixture0");
    // The first edit after reload must use the fetched document's revision.
    await page.getByLabel("Network interface").fill("fixture1");
    await page.getByRole("button", { name: "Save settings" }).click();
    await expect(page.getByText(/Settings saved/)).toBeVisible();
    expect((await call("get_dhcp")).config.interface).toBe("fixture1");
    await page.getByRole("button", { name: "Add reservation" }).click();
    await page.getByLabel("Reservation name").fill("lab-printer");
    await page.getByLabel("IP address", { exact: true }).fill("192.0.2.20");
    await page.getByLabel("Hostname (optional)").fill("lab-printer");
    await page
      .getByLabel("MAC address", { exact: true })
      .fill("02:00:00:00:00:10");
    await page.getByRole("button", { name: "Save reservation" }).click();
    await expect(page.getByText("Reservation change saved.", { exact: true })).toBeVisible();
    const reservations = await call("list_dhcp_reservations");
    expect(reservations.items[0].hostname).toBe("lab-printer");
    await call("update_dhcp_reservation", {
      id: "lab-printer",
      body: {
        revision: reservations.status.saved_revision,
        edits: [{ path: ["hostname"], value: "office-printer" }],
      },
    });
    await expect(
      page.getByRole("cell", { name: /office-printer/ }),
    ).toBeVisible({ timeout: 10000 });
    expect((await call("list_dhcp_leases")).runtime_available).toBe(false);
    await expect(
      page.getByText(/The live address list is unavailable/),
    ).toBeVisible();
    for (const width of [390, 1440]) {
      await page.setViewportSize({ width, height: 1100 });
      await page.getByRole("heading", { name: "DHCP", exact: true }).click();
      await page.evaluate(() => window.scrollTo(0, 0));
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
      await page.screenshot({
        path: testInfo.outputPath(`dhcp-${width}.png`),
        fullPage: true,
      });
    }
    await page
      .getByRole("button", { name: "Remove reservation lab-printer" })
      .click();
    await page.getByRole("button", { name: "Confirm removal" }).click();
    await expect(
      page.getByRole("cell", { name: /office-printer/ }),
    ).not.toBeVisible();
    expect((await call("list_dhcp_reservations")).items).toEqual([]);
    expect((await call("get_dhcp")).config.enabled).toBe(false);
  } finally {
    await agent.close();
  }
});
