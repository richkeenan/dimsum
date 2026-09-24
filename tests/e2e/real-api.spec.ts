import { readFile, writeFile, rename } from "node:fs/promises";
import { test, expect } from "../../web/e2e";
import { checkMCP } from "../../web/scripts/mcp-client-check.mjs";
test.skip(!process.env.DIMSUM_E2E_CONFIG, "Run through the isolated Go webassets browser harness");
test("real Go authentication, scalar text edit, collection writes, conflicts and file activation", async ({
  page,
}, testInfo) => {
  const file = process.env.DIMSUM_E2E_CONFIG!;
  const original = await readFile(file, "utf8");
  await page.goto("/settings");
  await page.getByLabel("Admin password").fill(process.env.DIMSUM_E2E_PASSWORD!);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Welcome to dimsum", exact: true }),
  ).not.toBeVisible();
  await expect(page.getByLabel("Maximum expired-answer age (seconds)")).toBeEnabled();
  await page.getByLabel("Token name", { exact: true }).fill("Browser test agent");
  await page.getByRole("button", { name: "Create token", exact: true }).click();
  const tokenField = page.getByLabel("New token", { exact: true });
  await expect(tokenField).toBeVisible();
  const agentToken = await tokenField.inputValue();
  expect(agentToken.length).toBeGreaterThan(32);
  await expect(page.getByRole("link", { name: "OpenAPI specification" })).toHaveCount(0);
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
  const compatibility = await checkMCP(new URL("/mcp", page.url()).href, agentToken);
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
    (await tools.json()).result.tools.some((tool: { name: string }) => tool.name === "get_summary"),
  ).toBe(true);
  await page.getByRole("button", { name: "Done", exact: true }).click();
  await expect(tokenField).not.toBeVisible();
  await page.reload();
  await expect(page.getByRole("button", { name: "Revoke Browser test agent" })).toBeVisible();
  await expect(tokenField).not.toBeVisible();
  const spec = await page.request.get("/api/v1/openapi.json", {
    headers: agentHeaders,
  });
  expect(spec.status()).toBe(200);
  expect((await spec.json()).openapi).toBe("3.1.0");
  await page.getByRole("button", { name: "Revoke Browser test agent" }).click();
  await expect(page.getByRole("button", { name: "Revoke Browser test agent" })).not.toBeVisible();
  const revoked = await page.request.post("/mcp", {
    headers: agentHeaders,
    data: { jsonrpc: "2.0", id: 3, method: "tools/list" },
  });
  expect(revoked.status()).toBe(401);
  await page.getByLabel("Maximum expired-answer age (seconds)").fill("120");
  await page.getByRole("button", { name: "Save settings" }).click();
  await expect(page.getByRole("button", { name: "Discard edits and reload" })).not.toBeVisible();
  const edited = await readFile(file, "utf8");
  expect(edited).toContain("# Isolated test input");
  expect(edited).toContain("max_stale_seconds: 120");
  expect(edited.split("\n").filter((l) => l.trim().startsWith("#"))).toEqual(
    original.split("\n").filter((l) => l.trim().startsWith("#")),
  );
  // This fixture has no DNS observation store. Seed an existing configured
  // device through the real shared API, then exercise its UI settings.
  const seeded = await page.evaluate(async () => {
    const current = await (await fetch("/api/v1/clients")).json();
    const result = await fetch("/api/v1/client-policy", {
      method: "PATCH",
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": sessionStorage.getItem("dimsum-csrf")!,
      },
      body: JSON.stringify({
        revision: current.status.saved_revision,
        scope: "client",
        id: "browser-workstation",
        create: true,
        name: "Browser workstation",
        selectors: { addresses: ["192.0.2.12"] },
      }),
    });
    return result.status;
  });
  expect(seeded).toBe(200);
  await page.getByRole("link", { name: "Devices", exact: true }).click();
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await expect(page).toHaveURL(/device=browser-workstation/);
  await page.getByLabel("DNS filtering", { exact: true }).selectOption("off");
  await page.getByRole("button", { name: "Save 1 change", exact: true }).click();
  await expect(page.getByRole("button", { name: "Save 0 changes", exact: true })).toBeVisible();
  let devicePolicy = await page.evaluate(async () =>
    (await fetch("/api/v1/client-policy?scope=client&id=browser-workstation")).json(),
  );
  expect(devicePolicy.desired.overrides.blocking).toBe(false);
  expect(devicePolicy.active.blocking).toEqual({
    value: false,
    source: { kind: "client", id: "browser-workstation" },
  });
  await page.getByLabel("DNS filtering", { exact: true }).selectOption("inherit");
  await page.getByRole("button", { name: "Save 1 change", exact: true }).click();
  await expect(page.getByRole("button", { name: "Save 0 changes", exact: true })).toBeVisible();
  devicePolicy = await page.evaluate(async () =>
    (await fetch("/api/v1/client-policy?scope=client&id=browser-workstation")).json(),
  );
  expect(devicePolicy.desired.overrides?.blocking).toBeUndefined();
  expect(devicePolicy.active.blocking.value).toBe(true);
  await page.getByRole("link", { name: "Filtering", exact: true }).click();
  await page.getByRole("link", { name: "Profiles & defaults", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Network defaults", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Create profile", exact: true }).click();
  await page.getByText("Advanced identification", { exact: true }).click();
  await page.getByLabel("Stable ID", { exact: true }).fill("browser-profile");
  await page.getByLabel("Name", { exact: true }).fill("Browser profile");
  await page.getByRole("button", { name: "Create profile", exact: true }).last().click();
  await expect(
    page.getByRole("heading", {
      name: "Edit Browser profile profile",
      exact: true,
    }),
  ).toBeVisible();
  await page.getByLabel("DNS filtering", { exact: true }).selectOption("off");
  await page.getByRole("button", { name: "Save 1 change", exact: true }).click();
  await expect(page.getByRole("button", { name: "Save 0 changes", exact: true })).toBeVisible();
  await page.goto("/clients?device=browser-workstation");
  await page.getByLabel("Profile", { exact: true }).selectOption("browser-profile");
  await page.getByRole("button", { name: "Save 1 change", exact: true }).click();
  await expect(page.getByRole("button", { name: "Save 0 changes", exact: true })).toBeVisible();
  devicePolicy = await page.evaluate(async () =>
    (await fetch("/api/v1/client-policy?scope=client&id=browser-workstation")).json(),
  );
  expect(devicePolicy.active.blocking).toEqual({
    value: false,
    source: { kind: "profile", id: "browser-profile" },
  });
  expect(devicePolicy.desired.overrides?.blocking).toBeUndefined();
  await page.screenshot({
    path: testInfo.outputPath("real-device-policy.png"),
    fullPage: true,
  });
  await page.goto("/profiles");
  await page.getByRole("button", { name: "Profiles", exact: true }).click();
  await page.getByRole("button", { name: "Edit Browser profile profile" }).click();
  await page.getByRole("button", { name: "Delete profile", exact: true }).click();
  await page.getByRole("button", { name: "Confirm delete", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText(/profile|referenced|assigned/i);
  await page.goto("/clients?device=browser-workstation");
  await page.getByLabel("Profile", { exact: true }).selectOption("");
  await page.getByRole("button", { name: "Save 1 change", exact: true }).click();
  await expect(page.getByRole("button", { name: "Save 0 changes", exact: true })).toBeVisible();
  await page.getByText("Advanced: upstream servers", { exact: true }).click();
  await page.getByLabel("Primary servers", { exact: true }).fill("192.0.2.53:53");
  await page.getByRole("button", { name: "Pause 5 min", exact: true }).click();
  await page.getByRole("button", { name: "Save 2 changes", exact: true }).click();
  await expect(page.getByRole("button", { name: "Save 0 changes", exact: true })).toBeVisible();
  devicePolicy = await page.evaluate(async () =>
    (await fetch("/api/v1/client-policy?scope=client&id=browser-workstation")).json(),
  );
  expect(devicePolicy.active.filtering).toBe(false);
  expect(devicePolicy.active.upstream.upstreams).toEqual(["192.0.2.53:53"]);
  await page.getByRole("button", { name: "Reset all overrides", exact: true }).click();
  await page.getByRole("button", { name: "Save 2 changes", exact: true }).click();
  await expect(page.getByRole("button", { name: "Save 0 changes", exact: true })).toBeVisible();
  devicePolicy = await page.evaluate(async () =>
    (await fetch("/api/v1/client-policy?scope=client&id=browser-workstation")).json(),
  );
  expect(devicePolicy.active.filtering).toBe(true);
  expect(devicePolicy.desired.overrides?.upstream).toBeUndefined();
  await page.goto("/profiles");
  await page.getByRole("button", { name: "Profiles", exact: true }).click();
  await page.getByRole("button", { name: "Edit Browser profile profile" }).click();
  await page.getByRole("button", { name: "Delete profile", exact: true }).click();
  await page.getByRole("button", { name: "Confirm delete", exact: true }).click();
  await expect(
    page.getByRole("heading", {
      name: "Edit Browser profile profile",
      exact: true,
    }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Network defaults", exact: true }).click();
  await page.getByLabel("DNS filtering", { exact: true }).selectOption("off");
  await page.getByRole("button", { name: "Save 1 change", exact: true }).click();
  await expect(page.getByRole("button", { name: "Save 0 changes", exact: true })).toBeVisible();
  devicePolicy = await page.evaluate(async () =>
    (await fetch("/api/v1/client-policy?scope=client&id=browser-workstation")).json(),
  );
  expect(devicePolicy.active.blocking).toEqual({ value: false, source: {} });
  await page.getByLabel("DNS filtering", { exact: true }).selectOption("on");
  await page.getByRole("button", { name: "Save 1 change", exact: true }).click();
  await expect(page.getByRole("button", { name: "Save 0 changes", exact: true })).toBeVisible();
  // Exercise the actual shared list API through the UI, including saved YAML
  // and active membership rather than only intercepted browser responses.
  await page.goto("/lists");
  await page.getByRole("button", { name: /Compatibility.*used by default/ }).click();
  await page.getByRole("checkbox", { name: "Work tools compatibility", exact: true }).check();
  await page.getByRole("button", { name: "Edit domains", exact: true }).click();
  const builtinDialog = page.getByRole("dialog", { name: "Work tools compatibility" });
  await builtinDialog.getByLabel("Domain to allow").fill("telemetry.example");
  await builtinDialog.getByRole("button", { name: "Add domain", exact: true }).click();
  await expect(builtinDialog.getByLabel("Domain to allow")).toHaveValue("");
  await builtinDialog.getByLabel("Search domains").fill("telemetry.example");
  await expect(builtinDialog.getByText("telemetry.example", { exact: true })).toBeVisible();
  expect(await readFile(file, "utf8")).toContain("telemetry.example");
  await builtinDialog
    .getByRole("button", { name: "Remove telemetry.example", exact: true })
    .click();
  await expect(builtinDialog.getByText("No matching domains.")).toBeVisible();
  await builtinDialog.getByLabel("Domain to allow").fill("metrics.example");
  await builtinDialog.getByRole("button", { name: "Add domain", exact: true }).click();
  await expect(builtinDialog.getByLabel("Domain to allow")).toHaveValue("");
  await builtinDialog
    .getByRole("button", { name: "Reset to shipped defaults", exact: true })
    .click();
  await builtinDialog.getByRole("button", { name: "Confirm reset", exact: true }).click();
  await expect(builtinDialog.getByText(/allowed domains · Shipped defaults/)).toBeVisible();
  const builtinRead = await page.evaluate(async () => {
    const lists = await (await fetch("/api/v1/lists")).json();
    const subscription = lists.items.find(
      (s: { url: string }) => s.url === "builtin://work-compatibility",
    );
    const response = await fetch(`/api/v1/builtin-lists/${encodeURIComponent(subscription.id)}`);
    if (!response.ok) throw new Error(`List readback failed: ${response.status}`);
    return response.json();
  });
  expect(builtinRead.customized).toBe(false);
  expect(builtinRead.status.active_revision).toBe(builtinRead.revision);
  expect(
    builtinRead.status.sources.find((s: { id: string }) => s.id === builtinRead.id).usable,
  ).toBe(true);
  await builtinDialog.getByRole("button", { name: "Done", exact: true }).click();
  await page.getByRole("link", { name: "Custom rules", exact: true }).click();
  await page.getByRole("button", { name: "Add rule" }).click();
  await page.getByLabel("Domain or pattern", { exact: true }).fill("ads.example.test");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  const rules = await page.evaluate(async () => (await fetch("/api/v1/rules")).json());
  const rule = rules.items.find((item: { pattern: string }) => item.pattern === "ads.example.test");
  expect(rule?.id).toBeTruthy();
  await page.getByLabel("Domain", { exact: true }).fill("ads.example.test");
  await page.getByRole("button", { name: "Test rule", exact: true }).click();
  await expect(page.getByRole("status").getByText("Blocked", { exact: true })).toBeVisible();
  await page.getByText("Match details", { exact: true }).click();
  await expect(page.getByText(`custom:${rule.id}`, { exact: false })).toBeVisible();
  await page.getByRole("link", { name: "Local DNS", exact: true }).click();
  await page.getByRole("button", { name: "Add record" }).click();
  await page.getByLabel("DNS name").fill("printer.home.arpa");
  await page.getByLabel("Address or target").fill("192.0.2.20");
  await page.getByLabel("Cache lifetime (seconds)").fill("60");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await expect(page.getByRole("cell", { name: "printer.home.arpa", exact: true })).toBeVisible();
  await page.getByRole("link", { name: "Upstreams", exact: true }).click();
  await page.getByRole("button", { name: "Add upstream" }).click();
  await page.getByLabel("DNS provider").selectOption("google");
  await expect(page.getByRole("radio", { name: /Encrypted/ })).toBeChecked();
  await page.getByRole("radio", { name: /Standard/ }).check();
  await page.screenshot({
    path: testInfo.outputPath("upstream-provider-desktop.png"),
  });
  await page.getByRole("button", { name: "Add provider" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await expect(
    page.getByRole("cell", {
      name: "Google Public DNS 8.8.8.8:53 Standard · unencrypted",
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("cell", {
      name: "Google Public DNS 8.8.4.4:53 Standard · unencrypted",
      exact: true,
    }),
  ).toBeVisible();
  expect(await readFile(file, "utf8")).toContain("# keep upstream comment");
  await page.getByRole("button", { name: "Add upstream" }).click();
  await page.getByLabel("DNS provider").selectOption("google");
  await page.getByRole("radio", { name: /Standard/ }).check();
  await expect(page.getByRole("button", { name: "Already configured" })).toBeDisabled();
  await page.getByLabel("DNS provider").selectOption("custom");
  await page.getByLabel("IP address", { exact: true }).fill("https://dns.example/dns-query");
  await page.getByRole("button", { name: "Add server" }).click();
  await expect(page.getByRole("alert")).toContainText("URLs and hostnames are not supported");
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: testInfo.outputPath("upstream-custom-mobile.png"),
  });
  const dialog = page.getByRole("dialog");
  expect(await dialog.evaluate((node) => node.scrollWidth <= node.clientWidth)).toBe(true);
  await page.getByLabel("IP address", { exact: true }).fill("192.0.2.53");
  await expect(page.getByLabel("Port", { exact: true })).toHaveValue("53");
  await page.getByRole("button", { name: "Add server" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await page.setViewportSize({ width: 1440, height: 1100 });
  await expect(
    page.getByRole("cell", {
      name: "Custom DNS server 192.0.2.53:53 Standard · unencrypted",
      exact: true,
    }),
  ).toBeVisible();
  await expect(page.getByLabel("Selection mode")).toHaveValue("ordered");
  await page.getByRole("button", { name: "Move 192.0.2.53:53 up", exact: true }).click();
  await expect(page.getByRole("row").nth(3)).toContainText("192.0.2.53:53");
  await page.reload();
  await expect(page.getByRole("row").nth(3)).toContainText("192.0.2.53:53");
  const reordered = await (await page.request.get("/api/v1/upstreams")).json();
  expect(reordered.items.slice(-2)).toEqual(["192.0.2.53:53", "8.8.4.4:53"]);
  await expect
    .poll(async () => {
      const current = await (await page.request.get("/api/v1/upstreams")).json();
      return current.status.active_revision;
    })
    .toBe(reordered.status.saved_revision);
  await page.getByLabel("Selection mode").selectOption("adaptive");
  await expect(page.getByRole("button", { name: /Move .* up/ })).toHaveCount(0);
  await page.reload();
  await expect(page.getByLabel("Selection mode")).toHaveValue("adaptive");
  const adaptive = await (await page.request.get("/api/v1/settings")).json();
  expect(adaptive.config.dns.upstream_policy.mode).toBe("adaptive");
  await expect
    .poll(async () => {
      const current = await (await page.request.get("/api/v1/settings")).json();
      return current.status.active_revision;
    })
    .toBe(adaptive.status.saved_revision);
  await page.screenshot({
    path: testInfo.outputPath("upstream-adaptive-desktop.png"),
  });
  await page.getByLabel("Selection mode").selectOption("ordered");
  await page.getByRole("button", { name: "Move 192.0.2.53:53 down", exact: true }).click();
  await expect(page.getByRole("row").last()).toContainText("192.0.2.53:53");
  expect(await readFile(file, "utf8")).toContain("# keep upstream comment");
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByLabel("Selection mode")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.screenshot({
    path: testInfo.outputPath("upstream-ordered-mobile.png"),
  });
  await page.setViewportSize({ width: 1440, height: 1100 });
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
      name: "Custom DNS server [2001:db8::53]:53 Standard · unencrypted",
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
  await page.getByRole("button", { name: "Discard edits and reload", exact: true }).click();
  await expect(page.getByLabel("Maximum expired-answer age (seconds)")).toHaveValue("240");
  await expect
    .poll(async () =>
      page.evaluate(async () => {
        const s = await (await fetch("/api/v1/settings")).json();
        return s.status.saved_revision === s.status.active_revision;
      }),
    )
    .toBe(true);
  await writeFile(file, (await readFile(file, "utf8")) + "unknown_setting: true\n");
  await page.getByRole("button", { name: "Reload displayed data" }).click();
  await expect(page.getByRole("alert")).toContainText("unknown_setting");
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Welcome to dimsum", exact: true })).toBeVisible();
});
