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
  const requests: URL[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/api/v1/performance?"))
      requests.push(new URL(request.url()));
  });
  page.on("pageerror", (error) => errors.push(error.message));
  await page.setViewportSize({ width: 1366, height: 768 });
  await page.goto("/?range=1h");
  await expect(page.getByText("≈ 36 ms", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("group", { name: "Interactive response-time chart" }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("region", { name: "Response time", exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("img", { name: /DNS outcomes/ })).toBeInViewport({
    ratio: 1,
  });
  await page.screenshot({
    path: testInfo.outputPath("overview-compact.png"),
    fullPage: true,
  });
  await expect(page.getByText("≈ 36 ms", { exact: true })).toBeVisible();
  expect(requests.at(-1)?.searchParams.get("from")).toBe(
    "2026-09-21T11:00:00.000Z",
  );
  expect(requests.at(-1)?.searchParams.get("to")).toBe(
    "2026-09-21T12:00:00.000Z",
  );
  expect(requests.at(-1)?.searchParams.get("resolution_seconds")).toBe("60");
  await page
    .getByRole("button", { name: /^View performance: Average response/ })
    .click();
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
  await expect(page.locator("#main").getByRole("status")).toContainText(
    "Partial coverage",
  );
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
  await expect
    .poll(() => requests.at(-1)?.searchParams.get("from"))
    .toBe("2026-09-14T12:00:00.000Z");
  expect(requests.at(-1)?.searchParams.get("resolution_seconds")).toBe("3600");
  expect(errors).toEqual([]);
});

test("pointer inspection uses elapsed time for clipped intervals", async ({
  page,
}) => {
  await page.route("**/api/v1/performance?**", (route) =>
    route.fulfill({
      json: {
        ...performance,
        points: [
          "2026-09-21T10:00:59Z",
          "2026-09-21T10:01:00Z",
          "2026-09-21T10:02:00Z",
        ].map((time, i) => ({
          ...performance.points[i],
          time,
          p95_us: ["1000", "2000", "3000"][i],
        })),
      },
    }),
  );
  await page.goto("/performance");
  const chart = page.getByRole("group", {
    name: "Interactive response-time chart",
  });
  const box = await chart.boundingBox();
  expect(box).not.toBeNull();
  await chart.hover({ position: { x: box!.width * 0.2, y: box!.height / 2 } });
  await expect(page.locator("#main").getByRole("status")).toContainText(
    "p95 ≈ 2 ms",
  );
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
