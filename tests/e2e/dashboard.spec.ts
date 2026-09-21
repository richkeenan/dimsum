import { test, expect } from "../../web/e2e";
import { fixtureAPI, query, summary } from "./fixtures";
test.beforeEach(async ({ page }) => fixtureAPI(page));
test("loading, empty, and offline states are distinct", async ({ page }) => {
  let release!: () => void;
  const wait = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/api/v1/queries?**", async (route) => {
    await wait;
    await route.fulfill({ json: { items: [], complete: true } });
  });
  await page.goto("/queries");
  await expect(
    page.getByRole("status").filter({ hasText: "Loading from the service" }),
  ).toBeVisible();
  release();
  await expect(
    page.getByRole("cell", { name: "No results for this selection." }),
  ).toBeVisible();
  await page.route("**/api/v1/queries?**", (route) => route.abort());
  await page.getByRole("button", { name: "Refresh all data" }).click();
  await expect(
    page.getByText("Cannot reach the administration service.", {
      exact: false,
    }),
  ).toBeVisible();
});
test("desktop dashboard, range consistency, dark mode and small screen", async ({
  page,
}, testInfo) => {
  await page.clock.setFixedTime(new Date("2026-09-21T12:00:00Z"));
  const ranges: string[] = [];
  page.on("request", (r) => {
    if (/\/(summary|rankings|timeseries)\?/.test(r.url()))
      ranges.push(new URL(r.url()).searchParams.get("from") ?? "");
  });
  await page.goto("/");
  await expect(page.getByText("48,216")).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Top clients" }),
  ).toBeVisible();
  await expect(page.getByText("Study laptop")).toBeVisible();
  await expect(page.getByRole("img", { name: /DNS outcomes/ })).toBeVisible();
  expect(new Set(ranges).size).toBe(1);
  await page.screenshot({
    path: testInfo.outputPath("desktop-light.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "Dark appearance" }).click();
  await expect(page.locator("html")).toHaveClass("dark");
  await page.screenshot({
    path: testInfo.outputPath("desktop-dark.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByRole("heading", { name: "Overview", exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("mobile.png"),
    fullPage: true,
  });
});
test("filtered cursors preserve detail position and scope is explicit", async ({
  page,
}) => {
  const requests: URL[] = [];
  await page.route("**/api/v1/queries?**", (route) => {
    const u = new URL(route.request().url());
    requests.push(u);
    return route.fulfill({
      json: {
        items: [
          {
            ...query,
            id: u.searchParams.get("cursor") ? "page-two" : query.id,
          },
        ],
        next_cursor: u.searchParams.get("cursor") ? undefined : "second-page",
      },
    });
  });
  await page.goto("/queries");
  await page.getByLabel("Filter name").fill("telemetry");
  await page.getByRole("button", { name: "Filter", exact: true }).click();
  await expect
    .poll(() => requests.at(-1)?.searchParams.get("name"))
    .toBe("telemetry");
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await expect
    .poll(() => requests.at(-1)?.searchParams.get("cursor"))
    .toBe("second-page");
  expect(requests.at(-1)?.searchParams.get("name")).toBe("telemetry");
  await page.getByRole("button", { name: "Previous" }).click();
  await page.getByRole("button", { name: query.name, exact: true }).click();
  await expect(
    page.getByText("Subdomains are not included.", { exact: false }),
  ).toBeVisible();
  await page.getByLabel("Match scope").selectOption("suffix");
  await expect(
    page.getByText("and every descendant", { exact: false }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await expect(page.getByText("Page 1 · up to 100 queries")).toBeVisible();
});
test("expired authentication returns to real data after login", async ({
  page,
}) => {
  let authenticated = false;
  await page.route("**/api/v1/summary?**", (route) =>
    route.fulfill({
      status: authenticated ? 200 : 401,
      json: authenticated
        ? summary
        : { code: "unauthorized", message: "Session expired" },
    }),
  );
  await page.route("**/session", (route) => {
    authenticated = true;
    return route.fulfill({ status: 204 });
  });
  await page.goto("/");
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.getByLabel("Admin password").fill("fixture-password");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await expect(page.getByText("48,216")).toBeVisible();
});
test("incomplete history and unavailable statistics do not imply DNS outage", async ({
  page,
}) => {
  await page.route("**/api/v1/summary?**", (route) =>
    route.fulfill({ json: { ...summary, complete: false } }),
  );
  await page.route("**/api/v1/rankings?**", (route) =>
    route.fulfill({
      status: 503,
      json: { code: "unavailable", message: "History storage unavailable" },
    }),
  );
  await page.goto("/");
  await expect(
    page.getByText("Incomplete history.", { exact: false }).first(),
  ).toBeVisible();
  await expect(page.getByText("History storage unavailable")).toBeVisible();
  await expect(page.getByText("Ready", { exact: true })).toBeVisible();
});
