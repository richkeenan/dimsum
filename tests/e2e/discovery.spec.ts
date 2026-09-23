import { test, expect } from "../../web/e2e";
import { activation, fixtureAPI, query, summary } from "./fixtures";

test("owner name and discovered device details coexist on mobile", async ({
  page,
}, testInfo) => {
  await fixtureAPI(page);
  let model = "Example model";
  await page.route("**/api/v1/clients?**", (route) =>
    route.fulfill({
      json: {
        status: activation,
        items: [
          {
            policy_id: "address:192.0.2.20",
            address: "192.0.2.20",
            name: "Owner television",
          },
        ],
        observed_available: true,
        observed: {
          items: [
            {
              address: "192.0.2.20",
              client_id: "address:192.0.2.20",
              matching_method: "address",
              name: "Discovered TV",
              name_source: "dns-sd",
              name_fresh: true,
              count: "12",
              blocked: "2",
              last_seen: "2026-09-21T12:00:00Z",
              device: {
                category: "tv",
                reason: "Advertised television model",
                inferred: false,
                fresh: true,
                hostname: "example-tv.local",
                manufacturer: "Example",
                model,
                evidence: [],
              },
            },
          ],
          complete: true,
          truncated: false,
        },
      },
    }),
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/clients");
  await expect(
    page.getByText("Owner television", { exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("TV", { exact: true })).toBeVisible();
  await expect(page.getByText("Discovered TV", { exact: true })).toHaveCount(0);
  await page
    .getByRole("button", { name: "Device details for Owner television" })
    .click();
  await expect(page.getByRole("dialog")).toContainText("example-tv.local");
  await expect(page.getByRole("dialog")).toContainText(
    "Advertised television model",
  );
  model = "Updated television model";
  await page.evaluate(() =>
    window.dispatchEvent(new Event("configuration-changed")),
  );
  await expect(page.getByRole("dialog")).toContainText(model);
  // The live observation window also re-renders the containing client page.
  await page.waitForResponse((response) =>
    response.url().includes("/api/v1/clients?"),
  );
  await expect(page.getByRole("dialog")).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  for (const width of [390, 1280]) {
    await page.setViewportSize({ width, height: 844 });
    for (const theme of ["light", "dark"]) {
      await page.evaluate(
        (theme) =>
          document.documentElement.classList.toggle("dark", theme === "dark"),
        theme,
      );
      await page.screenshot({
        path: testInfo.outputPath(`device-${width}-${theme}.png`),
        fullPage: true,
      });
    }
  }
  await page.getByRole("dialog").getByRole("button", { name: "Close" }).click();
  await page
    .getByRole("button", { name: "View queries for Owner television" })
    .click();
  await expect(page).toHaveURL(/\/queries\?.*client=192\.0\.2\.20/);
  await expect(page.getByLabel("Filter client")).toHaveValue(
    "Owner television (192.0.2.20)",
  );
});

test("discovery settings expose a revision-checked interface array", async ({
  page,
}) => {
  await fixtureAPI(page);
  let body: unknown;
  await page.route("**/api/v1/settings", async (route) => {
    if (route.request().method() === "PATCH") {
      body = route.request().postDataJSON();
      return route.fulfill({ json: activation });
    }
    return route.fulfill({ json: { status: activation, config: {} } });
  });
  await page.goto("/settings");
  await page.getByLabel("Discover device names with mDNS / Bonjour").check();
  await page.getByLabel("LAN interfaces").fill("eth0\nwlan0");
  await page.getByRole("button", { name: "Save discovery settings" }).click();
  await expect(
    page.getByRole("status").filter({ hasText: "Discovery settings saved" }),
  ).toBeVisible();
  expect(body).toEqual({
    revision: activation.saved_revision,
    edits: [
      { path: ["naming", "mdns", "enabled"], value: true },
      { path: ["naming", "mdns", "interfaces"], value: ["eth0", "wlan0"] },
    ],
  });
});

test("DNS guesses are labelled on clients, queries and overview with inspectable clues", async ({
  page,
}, testInfo) => {
  await fixtureAPI(page);
  const device = {
    category: "camera",
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
  await page.route("**/api/v1/clients?**", (route) =>
    route.fulfill({
      json: {
        status: activation,
        items: [],
        observed_available: true,
        observed: {
          items: [
            {
              address: "192.0.2.20",
              name: "Ring device",
              name_source: "dns-guess",
              name_fresh: true,
              count: "3",
              blocked: "0",
              device,
            },
          ],
        },
      },
    }),
  );
  await page.route("**/api/v1/queries?**", (route) =>
    route.fulfill({
      json: {
        items: [
          {
            ...query,
            client: "192.0.2.20",
            client_name: "Ring device",
            client_name_source: "dns-guess",
            client_device: device,
          },
        ],
      },
    }),
  );
  await page.route("**/api/v1/rankings?**", (route) =>
    route.fulfill({
      json: {
        complete: true,
        range: summary.range,
        updated_at: summary.updated_at,
        clients: [
          { address: "192.0.2.20", name: "Ring device", count: "3", device },
        ],
        domains: [],
      },
    }),
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/clients");
  await expect(page.getByText("DNS guess", { exact: true })).toBeVisible();
  await page
    .getByRole("button", { name: "Device details for Ring device" })
    .click();
  await expect(page.getByRole("dialog")).toContainText("DNS query clues");
  await expect(page.getByRole("dialog")).toContainText(
    "fw-eventstream.ring.com",
  );
  await page.screenshot({
    path: testInfo.outputPath("dns-guess-mobile.png"),
    fullPage: true,
  });
  await page.getByRole("dialog").getByRole("button", { name: "Close" }).click();
  await page
    .getByRole("button", { name: "View queries for Ring device" })
    .click();
  await expect(page.getByText("DNS guess", { exact: true })).toBeVisible();
  await page.goto("/overview");
  await expect(page.getByText("DNS guess", { exact: true })).toBeVisible();
});
