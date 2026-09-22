import { test, expect } from "../../web/e2e";
import { fixtureAPI, performance } from "./fixtures";

test.beforeEach(async ({ page }) => {
  await fixtureAPI(page);
  await page.clock.setFixedTime(new Date("2026-09-21T12:00:00Z"));
});

test("dashboard links to same-range performance with charts, keyboard data and responsive layouts", async ({
  page,
}, testInfo) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/?range=1h");
  await expect(
    page.getByRole("heading", { name: "Response time", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("≈ 36 ms", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "View performance" }).click();
  await expect(page).toHaveURL(/\/performance\?range=1h/);
  await expect(
    page.getByRole("heading", { name: "Performance", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("23,000 queries observed")).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "By query outcome" }),
  ).toBeVisible();
  const chart = page.getByRole("group", {
    name: "Interactive response-time chart",
  });
  await chart.focus();
  await page.keyboard.press("End");
  await expect(page.getByRole("status")).toContainText("Partial coverage");
  await page.getByText("View timing data", { exact: true }).click();
  await expect(
    page.getByRole("cell", { name: "Missing", exact: true }),
  ).toBeVisible();
  await page.getByText("View timing data", { exact: true }).click();
  await page.screenshot({
    path: testInfo.outputPath("performance-desktop.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "Dark appearance" }).click();
  await page.screenshot({
    path: testInfo.outputPath("performance-dark.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("performance-mobile.png"),
    fullPage: true,
  });
  await page.getByLabel("Time range").selectOption("7d");
  await expect(page).toHaveURL(/range=7d/);
  expect(errors).toEqual([]);
});

test("legacy precision, idle data and unavailable history are explicit", async ({
  page,
}) => {
  const empty = {
    count: "0",
    average_us: null,
    p50_us: null,
    p95_us: null,
    p99_us: null,
    percentiles_available: false,
  };
  await page.route("**/api/v1/performance?**", (route) =>
    route.fulfill({
      json: {
        ...performance,
        summary: {
          ...performance.summary,
          p50_us: null,
          p95_us: null,
          p99_us: null,
          percentiles_available: false,
        },
        points: performance.points.map((p) => ({
          ...p,
          ...empty,
          complete: true,
          gap: false,
        })),
      },
    }),
  );
  await page.goto("/performance");
  await expect(page.getByText(/some older history/)).toBeVisible();
  await expect(page.getByText(/No response-time observations/)).toBeVisible();
  await page.route("**/api/v1/performance?**", (route) =>
    route.fulfill({
      status: 503,
      json: {
        error: { code: "unavailable", message: "History storage unavailable" },
      },
    }),
  );
  await page.reload();
  await expect(page.getByRole("alert")).toContainText(
    "History storage unavailable",
  );
});
