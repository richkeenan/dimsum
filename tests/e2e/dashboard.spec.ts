import { test, expect } from "../../web/e2e";
import { fixtureAPI, query, summary } from "./fixtures";
test.beforeEach(async ({ page }) => fixtureAPI(page));
test("loading, empty, and offline states are distinct", async ({ page }) => {
  await page.clock.setFixedTime(new Date("2026-09-21T12:00:00Z"));
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
    page.getByRole("status").filter({ hasText: "Loading…" }),
  ).toBeVisible();
  release();
  await page.getByRole("button", { name: "Pause live" }).click();
  await expect(
    page.getByRole("cell", { name: "No results for this selection." }),
  ).toBeVisible();
  await page.route("**/api/v1/queries?**", (route) => route.abort());
  await page.getByRole("button", { name: "Reload displayed data" }).click();
  await expect(
    page.getByText("Unable to refresh. Showing the last available data.", {
      exact: false,
    }),
  ).toBeVisible();
  await expect(page.getByRole("cell", { name: "No results for this selection." })).toBeVisible();
  await page.goto("/queries");
  await expect(page.getByRole("alert")).toContainText("Cannot reach the administration service.");
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
    return route.fulfill({ json: { csrf_token: "fixture-csrf" } });
  });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Welcome to dimsum" })).toBeVisible();
  await page.getByLabel("Admin password").fill("fixture-password");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Welcome to dimsum" })).not.toBeVisible();
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
    page.getByText("Some history is unavailable", { exact: true }).first(),
  ).not.toBeVisible();
  await expect(page.getByText("History storage unavailable")).toBeVisible();
  await expect(page.getByText("DNS is not ready.", { exact: true })).not.toBeVisible();
});

test("header copies a DNS IP without its port and exposes nonstandard ports", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.route("**/api/v1/diagnostics", route => route.fulfill({ json: {
    dns_ready: true,
    dns_addresses: ["192.0.2.53:53", "[2001:db8::53]:5353"],
    storage: { available: true },
  } }));
  await page.goto("/");
  await expect(page.getByText("192.0.2.53", { exact: true })).not.toBeVisible();
  const trigger = page.getByRole("button", { name: "DNS server", exact: true });
  await trigger.focus();
  await page.keyboard.press("Enter");
  await page.getByRole("button", { name: "Copy DNS address 192.0.2.53", exact: true }).click();
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe("192.0.2.53");
  await expect(page.getByText("Copied", { exact: true })).toBeVisible();
  await expect(page.getByText("Port 5353", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Copy DNS address 2001:db8::53", exact: true }).click();
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe("2001:db8::53");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog", { name: "DNS addresses" })).not.toBeVisible();
  await expect(trigger).toBeFocused();
  await trigger.click();
  await page.getByRole("heading", { name: "Overview", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "DNS addresses" })).not.toBeVisible();
});

test("paused filtering can be resumed from any page", async ({ page }) => {
  let enabled = false;
  await page.route("**/api/v1/blocking", route => {
    if (route.request().method() === "PUT") enabled = route.request().postDataJSON().enabled;
    return route.fulfill({ json: { enabled, pause_until: "2026-09-21T12:30:00Z" } });
  });
  await page.goto("/queries");
  await expect(page.getByText(/Filtering is paused for all devices/)).toBeVisible();
  await page.getByRole("button", { name: "Resume filtering", exact: true }).click();
  await expect.poll(() => enabled).toBe(true);
  await expect(page.getByText(/Filtering is paused for all devices/)).not.toBeVisible();
});

test("DNS popover fits a narrow screen and keeps IPv6 copy accessible", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 700 });
  await page.route("**/api/v1/diagnostics", route => route.fulfill({ json: {
    dns_ready: true,
    dns_addresses: ["[2001:db8:1234:5678:abcd:1234:5678:abcd]:53"],
    storage: { available: true },
  } }));
  await page.goto("/");
  await page.getByRole("button", { name: "DNS server", exact: true }).click();
  const popover = page.getByRole("dialog", { name: "DNS addresses" });
  await expect(popover).toBeVisible();
  const bounds = await popover.boundingBox();
  expect(bounds).not.toBeNull();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(375);
  await expect(popover.getByRole("button", { name: /^Copy DNS address/ })).toBeInViewport();
});

test("DNS popover explains when no client-facing address exists", async ({ page }) => {
  await page.route("**/api/v1/diagnostics", route => route.fulfill({ json: {
    dns_ready: true,
    dns_addresses: [],
    storage: { available: true },
  } }));
  await page.goto("/");
  await page.getByRole("button", { name: "DNS server", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "DNS addresses" })).toContainText("No client-facing address");
  await expect(page.getByRole("button", { name: /^Copy DNS address/ })).toHaveCount(0);
});

test("service faults appear and clear when diagnostics recover", async ({ page }) => {
  let healthy = false;
  await page.route("**/api/v1/diagnostics", route => route.fulfill({ json: {
    dns_ready: healthy,
    dns_addresses: ["192.0.2.53:53"],
    storage: { available: healthy },
  } }));
  await page.goto("/");
  await expect(page.getByRole("alert")).toContainText("DNS is not ready.");
  await expect(page.getByRole("alert")).toContainText("Statistics are unavailable.");
  healthy = true;
  await page.getByRole("button", { name: "Reload displayed data" }).click();
  await expect(page.getByRole("alert")).not.toBeVisible();
});

test("DNS copying works without the secure-context Clipboard API", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.goto("/");
  await page.evaluate(() => Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true }));
  await page.getByRole("button", { name: "DNS server", exact: true }).click();
  await page.getByRole("button", { name: "Copy DNS address 192.0.2.53", exact: true }).click();
  await expect(page.getByText("Copied", { exact: true })).toBeVisible();
  await page.evaluate(() => delete (navigator as unknown as Record<string, unknown>).clipboard);
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe("192.0.2.53");
});

test("live polling defaults on only for newest uninspected queries", async ({ page }) => {
  await page.clock.install();
  let requests = 0;
  await page.route("**/api/v1/queries?**", (route) => {
    requests++;
    return route.fulfill({ json: { items: [query], next_cursor: "second-page" } });
  });
  await page.goto("/queries");
  await expect(page.getByRole("button", { name: query.name, exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Pause live" })).toBeVisible();
  let before = requests;
  await page.clock.fastForward(2100);
  await expect.poll(() => requests).toBeGreaterThan(before);
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await expect(page.getByText("Page 2 · up to 100 queries")).toBeVisible();
  await expect(page.getByRole("button", { name: "Previous" })).toBeEnabled();
  await expect(page.getByRole("status")).toHaveText("Paused on older queries");
  before = requests;
  await page.clock.fastForward(6100);
  expect(requests).toBe(before);
  await page.getByRole("button", { name: "Previous" }).click();
  await expect(page.getByText("Page 1 · up to 100 queries")).toBeVisible();
  await page.getByRole("button", { name: query.name, exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText(query.name);
  before = requests;
  await page.clock.fastForward(6100);
  expect(requests).toBe(before);
  await page.getByRole("button", { name: "Close", exact: true }).click();
  before = requests;
  await page.clock.fastForward(2100);
  await expect.poll(() => requests).toBeGreaterThan(before);
});

test("ordinary numeric URL filters preserve exact identities and round-trip edits", async ({ page }) => {
  const requests: URL[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/api/v1/queries?")) requests.push(new URL(request.url()));
  });
  await page.goto("/queries?rule_id=9007199254740993&generation=2&qtype=1&boot_id=fixture-boot");
  await expect.poll(() => requests.at(-1)?.searchParams.get("rule_id")).toBe("9007199254740993");
  expect(requests.at(-1)?.searchParams.get("generation")).toBe("2");
  expect(requests.at(-1)?.searchParams.get("qtype")).toBe("1");
  expect(requests.at(-1)?.searchParams.get("boot_id")).toBe("fixture-boot");
  await page.getByText("Advanced filters", { exact: true }).click();
  await expect(page.getByLabel("Filter rule_id")).toHaveValue("9007199254740993");
  await page.getByLabel("Filter name").fill(query.name);
  await expect.poll(() => new URL(page.url()).searchParams.get("name")).toBe(query.name);
  await page.reload();
  await expect(page.getByLabel("Filter name")).toHaveValue(query.name);
  await expect.poll(() => requests.at(-1)?.searchParams.get("rule_id")).toBe("9007199254740993");
  await page.getByRole("button", { name: "Clear", exact: true }).click();
  await expect.poll(() => new URL(page.url()).searchParams.has("rule_id")).toBe(false);
  await expect.poll(() => requests.at(-1)?.searchParams.has("rule_id")).toBe(false);
});

test("query filters update automatically with a searchable client picker and aligned advanced fields", async ({ page }, testInfo) => {
  await page.goto("/queries");
  const domain = page.getByLabel("Filter name");
  const client = page.getByRole("combobox", { name: "Filter client" });
  const result = page.getByLabel("Filter outcome");
  await domain.fill("example.test");
  await expect.poll(() => new URL(page.url()).searchParams.get("name")).toBe("example.test");
  await client.fill("study");
  await page.getByRole("option", { name: /Study laptop.*192.0.2.12/ }).click();
  await expect.poll(() => new URL(page.url()).searchParams.get("client")).toBe("192.0.2.12");
  await result.selectOption("blocked");
  await expect.poll(() => new URL(page.url()).searchParams.get("outcome")).toBe("blocked");
  await client.fill("192.0.2.99");
  await client.press("ArrowDown");
  await client.press("Enter");
  await expect.poll(() => new URL(page.url()).searchParams.get("client")).toBe("192.0.2.99");
  const before = await domain.boundingBox();
  await page.getByText("Advanced filters", { exact: true }).click();
  expect((await domain.boundingBox())?.y).toBe(before?.y);
  expect((await page.getByLabel("Filter qtype").boundingBox())!.y).toBeGreaterThan(before!.y + before!.height);
  for (const theme of ["light", "dark"]) {
    if (theme === "dark") await page.getByRole("button", { name: "Dark appearance" }).click();
    const backgrounds = await page.locator('form [data-slot="input"], form select').evaluateAll(els => els.map(el => getComputedStyle(el).backgroundColor));
    expect(new Set(backgrounds).size).toBe(1);
    expect(backgrounds[0]).not.toBe("rgba(0, 0, 0, 0)");
    await page.screenshot({ path: testInfo.outputPath(`query-filters-${theme}.png`), fullPage: true });
  }
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("query-filters-mobile.png"), fullPage: true });
  await page.getByRole("button", { name: "Clear", exact: true }).click();
  await expect(domain).toHaveValue("");
  await expect(client).toHaveValue("");
});

test("changing the range on page two resets the cursor and frozen bounds", async ({ page }) => {
  await page.clock.setFixedTime(new Date("2026-09-21T12:00:00Z"));
  const requests: URL[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/api/v1/queries?")) requests.push(new URL(request.url()));
  });
  await page.goto("/queries?name=telemetry.example.test");
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await expect.poll(() => requests.at(-1)?.searchParams.get("cursor")).toBe("second-page");
  await page.getByLabel("Time range").selectOption("1h");
  await expect(page.getByText("Page 1 · up to 100 queries")).toBeVisible();
  await expect.poll(() => requests.at(-1)?.searchParams.get("from")).toBe("2026-09-21T11:00:00.000Z");
  expect(requests.at(-1)?.searchParams.get("to")).toBe("2026-09-21T12:00:00.000Z");
  expect(requests.at(-1)?.searchParams.get("cursor")).toBeNull();
  expect(requests.at(-1)?.searchParams.get("name")).toBe(query.name);
  await expect(page.getByRole("button", { name: "Previous" })).toBeDisabled();
});

test("custom range controls follow direct URLs, reload, and browser history", async ({ page }) => {
  const from = "2026-09-20T12:00:00.000Z";
  const to = "2026-09-21T12:00:00.000Z";
  await page.goto(`/queries?range=custom&from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`);
  const local = await page.evaluate(({ from, to }) => {
    const input = (value: string) => {
      const date = new Date(value);
      return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
    };
    return { from: input(from), to: input(to) };
  }, { from, to });
  for (const reload of [false, true]) {
    if (reload) await page.reload();
    await expect(page.getByLabel("Time range")).toHaveValue("custom");
    await expect(page.getByLabel("From", { exact: true })).toHaveValue(local.from);
    await expect(page.getByLabel("To", { exact: true })).toHaveValue(local.to);
    await expect(page.getByRole("status").filter({ hasText: "Fixed time range" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Pause live" })).toBeDisabled();
  }
  await page.getByLabel("Time range").selectOption("1h");
  await expect(page.getByLabel("From", { exact: true })).not.toBeVisible();
  await page.goBack();
  await expect(page.getByLabel("Time range")).toHaveValue("custom");
  await expect(page.getByLabel("From", { exact: true })).toHaveValue(local.from);
  await expect(page.getByLabel("To", { exact: true })).toHaveValue(local.to);
  await page.goForward();
  await expect(page.getByLabel("Time range")).toHaveValue("1h");
  await expect(page.getByLabel("From", { exact: true })).not.toBeVisible();
});
