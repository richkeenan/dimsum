import { test, expect } from "../../web/e2e";
import { fixtureAPI } from "./fixtures";
test.beforeEach(async ({ page }) => fixtureAPI(page));

test("traffic inspection distinguishes missing intervals from zero and retains exact counts", async ({
  page,
}) => {
  await page.route("**/api/v1/timeseries?**", async (route) => {
    await route.fulfill({
      json: {
        complete: false,
        resolution_seconds: 3600,
        range: { from: "2026-09-20T12:00:00Z", to: "2026-09-20T16:00:00Z" },
        updated_at: "2026-09-20T16:00:00Z",
        points: [
          {
            time: "2026-09-20T12:00:00Z",
            gap: false,
            complete: true,
            outcomes: {
              local: "0",
              blocked: "20",
              cache: "30",
              stale: "0",
              forwarded: "50",
              error: "0",
              rejected: "0",
            },
          },
          { time: "2026-09-20T13:00:00Z", gap: true, complete: false, outcomes: null },
          {
            time: "2026-09-20T14:00:00Z",
            gap: false,
            complete: true,
            outcomes: {
              local: "0",
              blocked: "0",
              cache: "0",
              stale: "0",
              forwarded: "0",
              error: "0",
              rejected: "0",
            },
          },
          {
            time: "2026-09-20T15:00:00Z",
            gap: false,
            complete: false,
            outcomes: {
              local: "0",
              blocked: "0",
              cache: "0",
              stale: "0",
              forwarded: "9007199254740993",
              error: "0",
              rejected: "0",
            },
          },
        ],
      },
    });
  });
  await page.goto("/overview?range=24h");
  const chart = page.locator('svg[aria-label="Query activity: DNS outcomes over time"]');
  await chart.focus();
  const inspection = page.locator(".ts-chart-tooltip");
  await expect(inspection).toContainText("Forwarded: 50");
  await page.keyboard.press("ArrowRight");
  await expect(inspection).toContainText("Missing interval");
  await page.keyboard.press("ArrowRight");
  await expect(inspection).toContainText("Forwarded: 0");
  await page.keyboard.press("ArrowRight");
  await expect(inspection).toContainText("Partial coverage");
  await expect(inspection).toContainText("9,007,199,254,740,993");
});
