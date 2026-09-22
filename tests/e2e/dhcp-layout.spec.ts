import { test, expect, type Page } from "../../web/e2e";
import { activation, fixtureAPI } from "./fixtures";
import type { DHCPSettings } from "../../web/src/lib/api";

async function dhcpFixture(page: Page) {
  await fixtureAPI(page);
  const config: DHCPSettings = {
    enabled: false,
    interface: "",
    server_ip: "",
    subnet: "",
    gateway: "",
    range_start: "",
    range_end: "",
    local_domain: "",
    lease_seconds: 0,
    max_leases: 0,
    reservations: [],
  };
  const writes: {
    revision: string;
    edits: { path: string[]; value: unknown }[];
  }[] = [];
  let revision = "fixture-revision-1";
  await page.route("**/api/v1/dhcp**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() === "PATCH") {
      const body = route.request().postDataJSON();
      writes.push(body);
      if (body.revision !== revision) {
        await route.fulfill({
          status: 409,
          json: { code: "revision_conflict", message: "Settings changed" },
        });
        return;
      }
      for (const edit of body.edits)
        Object.assign(config, { [edit.path[0]]: edit.value });
      revision = `fixture-revision-${writes.length + 1}`;
      await route.fulfill({
        json: {
          ...activation,
          saved_revision: revision,
          active_revision: revision,
        },
      });
      return;
    }
    const status = {
      ...activation,
      saved_revision: revision,
      active_revision: revision,
    };
    if (path.endsWith("/status")) {
      await route.fulfill({
        json: {
          status,
          runtime_available: true,
          dhcp: {
            state: config.enabled ? "running" : "disabled",
            desired_enabled: config.enabled,
            applied_enabled: config.enabled,
            desired_generation: "42",
            applied_generation: "42",
            desired_interface: config.interface,
            interface: config.interface,
            desired_server_ip: config.server_ip,
            server_ip: config.server_ip,
            runtime: {
              generation: "42",
              storage: config.enabled ? "healthy" : "closed",
              capacity: "1024",
              held: config.enabled ? "1" : "0",
              pending: "0",
              clock_suspended: false,
              transport: {},
            },
          },
        },
      });
    } else if (path.endsWith("/leases")) {
      const expiry = new Date(Date.now() + 3600000).toISOString();
      await route.fulfill({
        json: {
          runtime_available: config.enabled,
          items: config.enabled
            ? [
                {
                  address: "192.0.2.100",
                  hostname: "office-laptop",
                  mac: "02:00:00:00:00:10",
                  state: "bound",
                  expiry,
                  hold_until: expiry,
                },
              ]
            : [],
        },
      });
    } else if (path.endsWith("/reservations")) {
      await route.fulfill({ json: { status, items: [] } });
    } else {
      await route.fulfill({ json: { status, config } });
    }
  });
  return writes;
}

for (const width of [390, 1440]) {
  test(`DHCP setup and enable flow at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 1100 });
    const writes = await dhcpFixture(page);
    await page.goto("/dhcp");
    await expect(
      page.getByRole("heading", { name: "DHCP is off" }),
    ).toBeVisible();
    await expect(
      page.getByRole("switch", { name: "Enable DHCP" }),
    ).not.toBeChecked();
    await page.screenshot({
      path: testInfo.outputPath(`dhcp-setup-${width}.png`),
      fullPage: true,
    });
    await page.getByLabel("Network interface").fill("eth0");
    await page
      .getByLabel("Server IP address", { exact: true })
      .fill("192.0.2.2");
    await page.getByLabel("Router IP address").fill("192.0.2.1");
    await page.getByLabel("Subnet", { exact: true }).fill("192.0.2.0/24");
    await page.getByLabel("First IP address").fill("192.0.2.100");
    await page.getByLabel("Last IP address").fill("192.0.2.199");
    await page
      .getByLabel("Lease duration", { exact: true })
      .selectOption("43200");
    await page.getByLabel("Local domain", { exact: true }).fill("home.arpa");
    await page.getByRole("switch", { name: "Enable DHCP" }).check();
    await page.getByRole("button", { name: "Save and enable" }).click();
    await expect(
      page.getByRole("heading", { name: "DHCP is on" }),
    ).toBeVisible();
    expect(writes).toHaveLength(1);
    expect(writes[0].edits).toEqual(
      expect.arrayContaining([
        { path: ["enabled"], value: true },
        { path: ["lease_seconds"], value: 43200 },
      ]),
    );
    await expect(
      page.getByText("office-laptop", { exact: true }),
    ).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({
      path: testInfo.outputPath(`dhcp-running-${width}.png`),
      fullPage: true,
    });
    await page.getByText("Advanced settings", { exact: true }).click();
    await expect(page.getByLabel("Maximum leases")).toBeVisible();
    await page.getByText("Troubleshooting", { exact: true }).click();
    await expect(
      page.getByRole("button", { name: "Check setup", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByLabel("Look for other DHCP servers"),
    ).not.toBeChecked();
  });
}
