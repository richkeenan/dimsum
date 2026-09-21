import { test, expect } from "../../web/e2e";
import { activation, fixtureAPI } from "./fixtures";

test("owner name and discovered device details coexist on mobile", async ({
  page,
}, testInfo) => {
  await fixtureAPI(page);
  await page.route("**/api/v1/clients?**", (route) =>
    route.fulfill({
      json: {
        status: activation,
        items: [{ address: "192.0.2.20", name: "Owner television" }],
        observed_available: true,
        observed: {
          items: [
            {
              address: "192.0.2.20",
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
                model: "Example model",
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
