import { test, expect } from "../../web/e2e";
import { fixtureAPI } from "./fixtures";

test.beforeEach(async ({ page }) => fixtureAPI(page));

for (const selection of [
  { range: "7d" },
  { range: "custom", from: "2026-09-18T12:00:00Z", to: "2026-09-25T12:00:00Z" },
]) {
  test(`device query drill-down preserves ${selection.range} from Overview`, async ({ page }) => {
    const search = new URLSearchParams({ range: selection.range });
    if (selection.from && selection.to) {
      search.set("from", selection.from);
      search.set("to", selection.to);
    }
    await page.goto(`/overview?${search}`);
    await page
      .getByRole("navigation", { name: "Main navigation", exact: true })
      .getByRole("link", { name: "Devices", exact: true })
      .click();
    await expect(page.getByLabel("Time range")).toHaveValue(selection.range);
    const queries = page.waitForRequest((request) => {
      const url = new URL(request.url());
      return url.pathname === "/api/v1/queries" && url.searchParams.has("client");
    });
    await page.getByRole("button", { name: "View queries for Study laptop", exact: true }).click();
    await expect(page.getByLabel("Time range")).toHaveValue(selection.range);
    const url = new URL(page.url());
    expect(url.searchParams.get("client")).toBe("192.0.2.12");
    for (const [key, value] of search) expect(url.searchParams.get(key)).toBe(value);
    const requested = new URL((await queries).url()).searchParams;
    if (selection.range === "custom") {
      expect(Date.parse(requested.get("from")!)).toBe(Date.parse(selection.from!));
      expect(Date.parse(requested.get("to")!)).toBe(Date.parse(selection.to!));
    } else {
      expect(Date.parse(requested.get("to")!) - Date.parse(requested.get("from")!)).toBe(
        7 * 24 * 60 * 60 * 1000,
      );
    }
  });
}

test("grouped navigation preserves deep links and the selected time range", async ({ page }) => {
  await page.goto("/performance?range=7d");
  const main = page.getByRole("navigation", { name: "Main navigation", exact: true });
  await expect(main.getByRole("link", { name: "Overview", exact: true })).toHaveAttribute(
    "aria-current",
    "true",
  );
  const sections = page.getByRole("navigation", { name: "Overview sections" });
  await expect(sections.getByRole("link", { name: "Performance" })).toHaveAttribute(
    "aria-current",
    "page",
  );
  await sections.getByRole("link", { name: "Summary" }).click();
  await expect(page).toHaveURL(/\/overview\?range=7d$/);

  await main.getByRole("link", { name: "Filtering" }).click();
  await page
    .getByRole("navigation", { name: "Filtering sections" })
    .getByRole("link", { name: "Custom rules" })
    .click();
  await expect(page).toHaveURL(/\/rules\?range=7d$/);
  await expect(main.getByRole("link", { name: "Filtering" })).toHaveAttribute(
    "aria-current",
    "true",
  );
  await page.reload();
  await expect(
    page
      .getByRole("navigation", { name: "Filtering sections" })
      .getByRole("link", { name: "Custom rules" }),
  ).toHaveAttribute("aria-current", "page");

  await main.getByRole("link", { name: "Settings", exact: true }).click();
  await page
    .getByRole("navigation", { name: "Settings sections" })
    .getByRole("link", { name: "Backups" })
    .click();
  await expect(page.getByRole("heading", { name: "Back up your configuration" })).toBeVisible();
  await page.goBack();
  await expect(
    page
      .getByRole("navigation", { name: "Settings sections" })
      .getByRole("link", { name: "General" }),
  ).toHaveAttribute("aria-current", "page");
});

test("mobile navigation closes after choosing a grouped page", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/diagnostics");
  await page.getByRole("button", { name: "Open navigation" }).click();
  const main = page.getByRole("navigation", { name: "Main navigation", exact: true });
  await expect(main.getByRole("link", { name: "Settings", exact: true })).toHaveAttribute(
    "aria-current",
    "true",
  );
  await main.getByRole("link", { name: "Filtering" }).click();
  await expect(page.locator("aside")).toBeHidden();
  await page
    .getByRole("navigation", { name: "Filtering sections" })
    .getByRole("link", { name: "Profiles & defaults" })
    .click();
  await expect(page.getByRole("button", { name: "Network defaults", exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test("keeps DNS server details in the desktop sidebar instead of the header", async ({ page }) => {
  await page.goto("/");

  const sidebar = page.locator("aside");
  await expect(sidebar.getByRole("button", { name: "DNS server" })).toBeVisible();
  await expect(page.locator("header").getByRole("button", { name: "DNS server" })).toHaveCount(0);
});

test("mobile brand navigation closes the menu within the Overview group", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/performance");
  await page.getByRole("button", { name: "Open navigation" }).click();
  await page.locator("aside").getByRole("link", { name: "dimsum DNS administration" }).click();
  await expect(page.locator("aside")).toBeHidden();
});
