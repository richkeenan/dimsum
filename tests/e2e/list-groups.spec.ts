import { test, expect } from "../../web/e2e";
import { fixtureAPI, activation } from "./fixtures";

test("list categories collapse, persist and remain usable on mobile and in dark mode", async ({
  page,
}) => {
  await fixtureAPI(page);
  const choices = [
    ["ads-trackers", "Example Ads Light"],
    ["ads-trackers", "Example Ads Normal"],
    ["security", "Example Threat Intelligence"],
    ["social-gambling", "Example Social Media"],
    ["social-gambling", "Example Gambling"],
    ["parental-control", "Example Adult Content"],
    ["compatibility", "Example Work Compatibility"],
  ];
  const catalog = choices.map(([category, label], i) => ({
    id: `preset-${i}`,
    category,
    label,
    url: `https://lists.example.test/${i}.txt`,
    description: "An optional curated domain list.",
    dialect: "dns-adblock",
    domain_kind: "suffix",
    available: true,
    unavailable_reason: "",
    default_enabled: false,
  }));
  await page.route("**/api/v1/catalog", (route) => route.fulfill({ json: { items: catalog } }));
  await page.route("**/api/v1/lists", (route) =>
    route.fulfill({
      json: {
        items: [
          { ...catalog[0], id: "renamed-subscription", enabled: true },
          { ...catalog[2], enabled: true, default_apply: false },
        ],
        status: {
          ...activation,
          sources: [{ id: "preset-2", enabled: true, usable: false, error: "Download failed" }],
        },
      },
    }),
  );
  await page.goto("/lists");
  const ads = page.getByRole("button", { name: /Ads & Trackers/ });
  const security = page.getByRole("button", { name: /Security & Scams/ });
  await expect(ads).toHaveAttribute("aria-expanded", "false");
  await expect(ads).toContainText("2 lists · 1 used by default");
  await expect(security).toContainText("1 issue");
  await expect(page.getByRole("checkbox", { name: "Example Ads Light" })).toHaveCount(0);
  await ads.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("checkbox", { name: "Example Ads Light" })).toBeChecked();
  await page.reload();
  await expect(ads).toHaveAttribute("aria-expanded", "true");
  await expect(security).toHaveAttribute("aria-expanded", "false");
  await page.getByRole("button", { name: "Expand all" }).click();
  await expect(page.getByRole("checkbox")).toHaveCount(7);
  await expect(page.getByText("Adult content", { exact: true })).toBeVisible();
  await page.screenshot({ path: "test-results/list-groups-expanded.png", fullPage: true });
  await page.getByRole("button", { name: "Collapse all" }).click();
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    for (const dark of [false, true]) {
      await page.evaluate((dark) => document.documentElement.classList.toggle("dark", dark), dark);
      await expect(ads).toBeInViewport();
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
      ).toBe(true);
      await page.screenshot({
        animations: "disabled",
        path: `test-results/list-groups-${width}-${dark ? "dark" : "light"}.png`,
        fullPage: true,
      });
    }
  }
  await security.click();
  await expect(page.getByText("Download failed", { exact: true }).first()).toBeVisible();
});
