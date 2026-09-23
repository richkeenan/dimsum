import { readFile } from "node:fs/promises";
import { createServer, request } from "node:http";
import { test as base, expect } from "../../web/e2e";
import { fixtureAPI, policyFixture } from "./fixtures";

const archiveURL = "/api/v1/config/backups/0123456789abcdef0123456789abcdef";
// Chromium downloads bypass page.route. Serve the archive over real HTTP while
// forwarding the UI to the preview server; other API responses remain fixtures.
const test = base.extend<{ downloadOrigin: string }>({
  downloadOrigin: async ({ baseURL }, use) => {
    const server = createServer((incoming, outgoing) => {
      if (incoming.url === archiveURL) {
        outgoing.writeHead(200, {
          "Content-Type": "application/x-tar",
          "Content-Disposition": 'attachment; filename="dimsum-config.tar"',
        });
        outgoing.end("synthetic configuration archive");
        return;
      }
      const proxy = request(new URL(incoming.url!, baseURL), (response) => {
        outgoing.writeHead(response.statusCode!, response.headers);
        response.pipe(outgoing);
      });
      proxy.on("error", () => {
        outgoing.writeHead(502);
        outgoing.end();
      });
      incoming.pipe(proxy);
    });
    await new Promise<void>((resolve) =>
      server.listen(0, "127.0.0.1", resolve),
    );
    try {
      const address = server.address() as { port: number };
      await use(`http://127.0.0.1:${address.port}`);
    } finally {
      server.closeAllConnections();
      await new Promise<void>((resolve) => server.close(() => resolve()));
    }
  },
});

for (const width of [1440, 390]) {
  test(`backup prepares and downloads in one click without disturbing the page at ${width}px`, async ({
    page,
    downloadOrigin,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 1100 });
    await fixtureAPI(page);
    let release!: () => void;
    const hold = new Promise<void>((resolve) => {
      release = resolve;
    });
    let started = false;
    let reads = 0;
    let posts = 0;
    const url = archiveURL;
    await page.route("**/api/v1/jobs", async (route) => {
      if (route.request().method() === "POST") {
        posts++;
        expect(route.request().postDataJSON()).toEqual({
          kind: "backup",
          input: {},
        });
        await hold;
        started = true;
        return route.fulfill({
          json: {
            id: "1",
            kind: "backup",
            state: "running",
            created: "2026-09-23T12:00:00Z",
          },
        });
      }
      if (started) reads++;
      return route.fulfill({
        json: {
          items: started
            ? [
                {
                  id: "1",
                  kind: "backup",
                  state: reads > 1 ? "succeeded" : "running",
                  created: "2026-09-23T12:00:00Z",
                  result: reads > 1 ? { download_url: url } : undefined,
                },
              ]
            : [],
        },
      });
    });
    await page.goto(`${downloadOrigin}/jobs`);
    await expect(page.getByText("No jobs have been recorded.")).toBeVisible();
    await page
      .getByText("Diagnostics and maintenance", { exact: true })
      .click();
    const input = page.getByLabel("Configured upstream (IP:port)");
    await input.fill("192.0.2.53:53");
    const original = await input.elementHandle();
    const restore = page.getByRole("heading", { name: "Restore a backup" });
    const position = await restore.boundingBox();
    const downloadPromise = page.waitForEvent("download");
    await page
      .getByRole("button", { name: "Download backup", exact: true })
      .click();
    await expect(
      page.getByRole("button", { name: "Preparing backup…" }),
    ).toBeDisabled();
    await expect(
      page.getByRole("button", { name: "Test upstream" }),
    ).toBeEnabled();
    await expect(
      page.getByRole("button", { name: "Validate and restore" }),
    ).toBeVisible();
    await expect(input).toBeEnabled();
    expect((await restore.boundingBox())?.y).toBe(position?.y);
    release();
    const download = await downloadPromise;
    expect(await download.failure()).toBeNull();
    expect(download.suggestedFilename()).toBe("dimsum-config.tar");
    const file = testInfo.outputPath("configuration.tar");
    await download.saveAs(file);
    expect(await readFile(file, "utf8")).toBe(
      "synthetic configuration archive",
    );
    await expect(input).toHaveValue("192.0.2.53:53");
    expect(await original!.evaluate((node) => node.isConnected)).toBe(true);
    expect((await restore.boundingBox())?.y).toBe(position?.y);
    await expect(page).toHaveURL(/\/jobs$/);
    expect(posts).toBe(1);
    await expect(
      page.getByText("Sorting applies to the loaded job history."),
    ).toHaveCount(0);
    await page.screenshot({
      path: testInfo.outputPath("backup-ready.png"),
      fullPage: true,
    });
  });
}

test("diagnostics explain outcomes and reveal technical detail only on demand", async ({
  page,
}, testInfo) => {
  await fixtureAPI(page);
  let failure = "";
  const discoveryError =
    "Interface 3: multicast send: IPv4=<nil> IPv6=network is unreachable";
  await page.route("**/api/v1/diagnostics", (route) =>
    route.fulfill({
      json: {
        dns_ready: true,
        naming: {
          enabled: true,
          running: true,
          interfaces: ["eth0"],
          errors: [discoveryError],
        },
        storage: {
          available: true,
          writer: {
            LastSuccess: "2026-09-23T12:00:00Z",
            LastError: failure,
            LostDetails: "0",
            Backlogged: false,
          },
        },
        upstreams: [{ Endpoint: "192.0.2.53:53", State: "closed" }],
      },
    }),
  );
  await page.route("**/api/v1/rules/test", (route) =>
    route.fulfill({
      json: {
        name: "example.test",
        normalized: "example.test",
        generation: "42",
        client_id: "",
        matching_method: "network",
        handling: "policy",
        effective: policyFixture.effective,
        decision: {
          result: "forward",
          generation: "42",
          rule_id: "",
          source_ids: [],
          scope: {},
        },
      },
    }),
  );
  await page.goto("/diagnostics");
  await expect(
    page.getByText("Query history saved", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText(discoveryError, { exact: true })).toHaveCount(0);
  await page.getByLabel("Domain", { exact: true }).fill("example.test");
  await page.getByRole("button", { name: "Check domain", exact: true }).click();
  await expect(page.getByRole("main").getByRole("status")).toContainText(
    "Not blocked by current rules",
  );
  await expect(page.getByRole("main").getByRole("status")).toContainText(
    "Network defaults",
  );
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 960 });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: testInfo.outputPath(`diagnostics-${width}.png`),
      fullPage: true,
    });
  }
  failure = "database or disk is full (13)";
  await page.getByRole("button", { name: "Refresh measurements" }).click();
  await expect(page.getByText(/Recent queries may be missing/)).toBeVisible();
  await expect(page.getByText(/Free up disk space/)).toBeVisible();
  await expect(page.getByText(failure, { exact: true })).toHaveCount(0);
  const button = page.getByRole("button", { name: "Query history details" });
  await button.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog")).toContainText(failure);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("history-error-details-mobile.png"),
    fullPage: true,
  });
  await page.keyboard.press("Escape");
  await expect(button).toBeFocused();
});
