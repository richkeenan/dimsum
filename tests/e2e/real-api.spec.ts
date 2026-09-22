import { readFile, writeFile, rename } from "node:fs/promises";
import { test, expect } from "../../web/e2e";
import { checkMCP } from "../../web/scripts/mcp-client-check.mjs";
test.skip(
  !process.env.DIMSUM_E2E_CONFIG,
  "Run through the isolated Go webassets browser harness",
);
test("real Go authentication, scalar text edit, collection writes, conflicts and file activation", async ({
  page,
}, testInfo) => {
  const file = process.env.DIMSUM_E2E_CONFIG!;
  const original = await readFile(file, "utf8");
  await page.goto("/settings");
  await page
    .getByLabel("Admin password")
    .fill(process.env.DIMSUM_E2E_PASSWORD!);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Welcome to dimsum", exact: true }),
  ).not.toBeVisible();
  await expect(
    page.getByLabel("Maximum expired-answer age (seconds)"),
  ).toBeEnabled();
  await page
    .getByLabel("Token name", { exact: true })
    .fill("Browser test agent");
  await page.getByRole("button", { name: "Create token", exact: true }).click();
  const tokenField = page.getByLabel("New token", { exact: true });
  await expect(tokenField).toBeVisible();
  const agentToken = await tokenField.inputValue();
  expect(agentToken.length).toBeGreaterThan(32);
  await expect(
    page.getByRole("link", { name: "OpenAPI specification" }),
  ).toHaveCount(0);
  // Localhost is a secure context; remove the modern API to exercise LAN HTTP copying.
  await page.evaluate(() => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: undefined,
    });
  });
  for (const name of ["Copy token", "Copy connection fields"]) {
    await page.getByRole("button", { name, exact: true }).click();
    await expect(
      page.getByRole("status").filter({ hasText: "Copied to clipboard." }),
    ).toBeVisible();
  }
  const compatibility = await checkMCP(
    new URL("/mcp", page.url()).href,
    agentToken,
  );
  expect(compatibility.tools).toBeGreaterThan(0);
  const agentHeaders = {
    Authorization: `Bearer ${agentToken}`,
    Accept: "application/json, text/event-stream",
  };
  const initialized = await page.request.post("/mcp", {
    headers: agentHeaders,
    data: {
      jsonrpc: "2.0",
      id: 1,
      method: "initialize",
      params: {
        protocolVersion: "2025-03-26",
        capabilities: {},
        clientInfo: { name: "browser-acceptance", version: "1" },
      },
    },
  });
  expect(initialized.status()).toBe(200);
  expect((await initialized.json()).result.serverInfo).toBeTruthy();
  const tools = await page.request.post("/mcp", {
    headers: agentHeaders,
    data: { jsonrpc: "2.0", id: 2, method: "tools/list" },
  });
  expect(tools.status()).toBe(200);
  expect(
    (await tools.json()).result.tools.some(
      (tool: { name: string }) => tool.name === "get_summary",
    ),
  ).toBe(true);
  await page.getByRole("button", { name: "Done", exact: true }).click();
  await expect(tokenField).not.toBeVisible();
  await page.reload();
  await expect(
    page.getByRole("button", { name: "Revoke Browser test agent" }),
  ).toBeVisible();
  await expect(tokenField).not.toBeVisible();
  const spec = await page.request.get("/api/v1/openapi.json", {
    headers: agentHeaders,
  });
  expect(spec.status()).toBe(200);
  expect((await spec.json()).openapi).toBe("3.1.0");
  await page.getByRole("button", { name: "Revoke Browser test agent" }).click();
  await expect(
    page.getByRole("button", { name: "Revoke Browser test agent" }),
  ).not.toBeVisible();
  const revoked = await page.request.post("/mcp", {
    headers: agentHeaders,
    data: { jsonrpc: "2.0", id: 3, method: "tools/list" },
  });
  expect(revoked.status()).toBe(401);
  await page.getByLabel("Maximum expired-answer age (seconds)").fill("120");
  await page.getByRole("button", { name: "Save settings" }).click();
  await expect(
    page.getByRole("status").filter({ hasText: "Settings saved." }),
  ).toBeVisible();
  const edited = await readFile(file, "utf8");
  expect(edited).toContain("# Isolated test input");
  expect(edited).toContain("max_stale_seconds: 120");
  expect(edited.split("\n").filter((l) => l.trim().startsWith("#"))).toEqual(
    original.split("\n").filter((l) => l.trim().startsWith("#")),
  );
  await page.getByRole("link", { name: "Devices", exact: true }).click();
  await page.getByRole("button", { name: "Name an address" }).click();
  await page.getByLabel("Observed address").fill("192.0.2.12");
  await page
    .getByLabel("Friendly name", { exact: true })
    .fill("Browser workstation");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await expect(
    page.getByRole("button", {
      name: "View queries for Browser workstation",
      exact: true,
    }),
  ).toContainText(/Browser workstation\s*192\.0\.2\.12/);
  await page.getByRole("link", { name: "Custom rules", exact: true }).click();
  await page.getByRole("button", { name: "Add rule" }).click();
  await page
    .getByLabel("Domain or pattern", { exact: true })
    .fill("ads.example.test");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  const rules = await page.evaluate(async () =>
    (await fetch("/api/v1/rules")).json(),
  );
  const rule = rules.items.find(
    (item: { pattern: string }) => item.pattern === "ads.example.test",
  );
  expect(rule?.id).toBeTruthy();
  await page.getByLabel("Domain", { exact: true }).fill("ads.example.test");
  await page.getByRole("button", { name: "Test rule", exact: true }).click();
  await expect(
    page.getByRole("status").getByText("Blocked", { exact: true }),
  ).toBeVisible();
  await page.getByText("Match details", { exact: true }).click();
  await expect(
    page.getByText(`custom:${rule.id}`, { exact: false }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Local DNS", exact: true }).click();
  await page.getByRole("button", { name: "Add record" }).click();
  await page.getByLabel("DNS name").fill("printer.home.arpa");
  await page.getByLabel("Address or target").fill("192.0.2.20");
  await page.getByLabel("Cache lifetime (seconds)").fill("60");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await expect(
    page.getByRole("cell", { name: "printer.home.arpa", exact: true }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Upstreams", exact: true }).click();
  await page.getByRole("button", { name: "Add upstream" }).click();
  await page.getByLabel("DNS provider").selectOption("google");
  await page.screenshot({
    path: testInfo.outputPath("upstream-provider-desktop.png"),
  });
  await page.getByRole("button", { name: "Add provider" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await expect(
    page.getByRole("cell", {
      name: "Google Public DNS 8.8.8.8:53",
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("cell", {
      name: "Google Public DNS 8.8.4.4:53",
      exact: true,
    }),
  ).toBeVisible();
  expect(await readFile(file, "utf8")).toContain("# keep upstream comment");
  await page.getByRole("button", { name: "Add upstream" }).click();
  await page.getByLabel("DNS provider").selectOption("google");
  await expect(
    page.getByRole("button", { name: "Already configured" }),
  ).toBeDisabled();
  await page.getByLabel("DNS provider").selectOption("custom");
  await page
    .getByLabel("IP address", { exact: true })
    .fill("https://dns.example/dns-query");
  await page.getByRole("button", { name: "Add server" }).click();
  await expect(page.getByRole("alert")).toContainText(
    "URLs and hostnames are not supported",
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: testInfo.outputPath("upstream-custom-mobile.png"),
  });
  const dialog = page.getByRole("dialog");
  expect(
    await dialog.evaluate((node) => node.scrollWidth <= node.clientWidth),
  ).toBe(true);
  await page.getByLabel("IP address", { exact: true }).fill("192.0.2.53");
  await expect(page.getByLabel("Port", { exact: true })).toHaveValue("53");
  await page.getByRole("button", { name: "Add server" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await page.setViewportSize({ width: 1440, height: 1100 });
  await expect(
    page.getByRole("cell", {
      name: "Custom DNS server 192.0.2.53:53",
      exact: true,
    }),
  ).toBeVisible();
  await page
    .getByRole("row")
    .filter({ hasText: "192.0.2.53:53" })
    .getByRole("button", { name: "Edit", exact: true })
    .click();
  await page.getByLabel("IP address", { exact: true }).fill("2001:db8::53");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await page
    .getByRole("row")
    .filter({ hasText: "[2001:db8::53]:53" })
    .getByRole("button", { name: "Edit", exact: true })
    .click();
  await page.getByRole("button", { name: "Remove server" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await expect(
    page.getByRole("cell", {
      name: "Custom DNS server [2001:db8::53]:53",
      exact: true,
    }),
  ).not.toBeVisible();
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  await page.getByLabel("Maximum expired-answer age (seconds)").fill("180");
  const beforeExternal = await readFile(file, "utf8");
  await writeFile(
    file + ".new",
    beforeExternal.replace("max_stale_seconds: 120", "max_stale_seconds: 240"),
  );
  await rename(file + ".new", file);
  await page.getByRole("button", { name: "Save settings" }).click();
  await expect(page.getByRole("alert")).toContainText(
    "Settings changed since you opened this form",
  );
  expect(await readFile(file, "utf8")).toContain("max_stale_seconds: 240");
  await page
    .getByRole("button", { name: "Discard edits and reload", exact: true })
    .click();
  await expect(
    page.getByLabel("Maximum expired-answer age (seconds)"),
  ).toHaveValue("240");
  await expect
    .poll(async () =>
      page.evaluate(async () => {
        const s = await (await fetch("/api/v1/settings")).json();
        return s.status.saved_revision === s.status.active_revision;
      }),
    )
    .toBe(true);
  await writeFile(
    file,
    (await readFile(file, "utf8")) + "unknown_setting: true\n",
  );
  await page.getByRole("button", { name: "Reload displayed data" }).click();
  await expect(page.getByRole("alert")).toContainText("unknown_setting");
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Welcome to dimsum", exact: true }),
  ).toBeVisible();
});
