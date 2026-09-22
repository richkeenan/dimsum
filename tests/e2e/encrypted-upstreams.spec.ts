import { test, expect } from "../../web/e2e";
import { fixtureAPI, settings, activation } from "./fixtures";

test("encrypted providers, saved custom URLs and measured tests fit desktop and mobile", async ({
  page,
}, testInfo) => {
  await fixtureAPI(page);
  let addresses = ["192.0.2.53:53"];
  const writes: any[] = [];
  await page.route("**/api/v1/settings", (route) =>
    route.fulfill({
      json: {
        ...settings,
        config: { ...settings.config, dns: { upstreams: addresses } },
      },
    }),
  );
  await page.route("**/api/v1/upstreams", (route) => {
    if (route.request().method() !== "GET") {
      const body = route.request().postDataJSON();
      writes.push(body);
      if (body.edits)
        addresses[Number(body.edits[0].path[0])] = body.edits[0].value;
      else
        addresses.push(
          typeof body.item === "string"
            ? body.item
            : "https://cloudflare-dns.com/dns-query",
        );
      return route.fulfill({ json: activation });
    }
    return route.fulfill({
      json: {
        status: activation,
        items: addresses.map((address) => ({ address })),
      },
    });
  });
  let healthy = true;
  await page.route("**/api/v1/jobs", (route) =>
    route.fulfill({
      json: {
        id: "1",
        kind: "upstream-probe",
        state: "succeeded",
        created: "2026-09-22T00:00:00Z",
        result: {
          healthy,
          responding: healthy,
          duration_us: "12500",
          transport: "https",
          ...(healthy ? {} : { error: "x509: certificate has expired" }),
        },
      },
    }),
  );
  await page.goto("/upstreams");
  await page.getByRole("button", { name: "Add upstream" }).click();
  await page.getByLabel("DNS provider").selectOption("cloudflare");
  await expect(page.getByRole("radio", { name: /Encrypted/ })).toBeChecked();
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    expect(
      await page
        .getByRole("dialog")
        .evaluate((el) => el.scrollWidth <= el.clientWidth),
    ).toBe(true);
    await page.screenshot({
      path: testInfo.outputPath(`provider-${width}.png`),
    });
  }
  // Native radios must also work from the keyboard.
  await page.getByRole("radio", { name: /Encrypted/ }).focus();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("radio", { name: /Standard/ })).toBeChecked();
  await page.keyboard.press("ArrowLeft");
  await page.getByRole("button", { name: "Add provider" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  expect(writes[0].item).toEqual({ preset: "cloudflare", transport: "https" });
  await expect(
    page.getByText(/Some lookups may be sent unencrypted/),
  ).toBeVisible();
  const provider = page
    .getByRole("row")
    .filter({ hasText: "https://cloudflare-dns.com/dns-query" });
  await provider.getByRole("button", { name: "Test connection" }).click();
  await expect(provider.getByRole("status")).toContainText(
    "Encrypted connection verified",
  );
  await expect(provider.getByRole("status")).toContainText("12.5 ms");
  healthy = false;
  await provider.getByRole("button", { name: "Test connection" }).click();
  await expect(provider.getByRole("status")).toContainText(
    "certificate has expired",
  );
  await expect(provider.getByRole("status")).not.toContainText("verified");
  await provider.getByRole("button", { name: "Edit", exact: true }).click();
  await expect(page.getByLabel("Encrypted server URL")).toHaveValue(
    "https://cloudflare-dns.com/dns-query",
  );
  await page
    .getByLabel("Encrypted server URL")
    .fill("tls://resolver.example:8853");
  await page.screenshot({ path: testInfo.outputPath("custom-mobile.png") });
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await expect(
    page.getByText("Encrypted · TLS (DoT)", { exact: true }),
  ).toBeVisible();
  expect(writes[1].edits).toEqual([
    { path: ["1"], value: "tls://resolver.example:8853" },
  ]);
  await page.getByRole("button", { name: "Add upstream" }).click();
  await page.getByLabel("DNS provider").selectOption("custom");
  await page
    .getByLabel("Encrypted server URL")
    .fill("https://other.example/dns-query?profile=test");
  await page.getByRole("button", { name: "Add server" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  expect(writes[2].item).toBe("https://other.example/dns-query?profile=test");
});

test("advanced bootstrap override and reset read back saved settings", async ({
  page,
}, testInfo) => {
  await fixtureAPI(page);
  let bootstrap: string[] | undefined;
  await page.route("**/api/v1/settings", (route) => {
    if (route.request().method() === "PATCH") {
      const body = route.request().postDataJSON();
      expect(body.revision).toBe(activation.saved_revision);
      expect(body.edits[0].path).toEqual(["dns", "bootstrap_dns"]);
      bootstrap = body.edits[0].value;
      return route.fulfill({ json: activation });
    }
    return route.fulfill({
      json: {
        ...settings,
        config: {
          ...settings.config,
          dns: { ...settings.config.dns, bootstrap_dns: bootstrap },
        },
      },
    });
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/settings");
  await page.getByText("Advanced: bootstrap DNS", { exact: true }).click();
  await page
    .getByLabel("Bootstrap DNS servers")
    .fill("192.0.2.53:53\n[2001:db8::53]:53");
  await page.getByRole("button", { name: "Save bootstrap DNS" }).click();
  await expect(page.getByText(/Bootstrap DNS saved/)).toBeVisible();
  await expect(page.getByLabel("Bootstrap DNS servers")).toHaveValue(
    "192.0.2.53:53\n[2001:db8::53]:53",
  );
  await page.getByLabel("Bootstrap DNS servers").scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath("bootstrap-mobile.png") });
  await page.getByRole("button", { name: "Use automatic defaults" }).click();
  await page.getByRole("button", { name: "Save bootstrap DNS" }).click();
  await expect.poll(() => bootstrap).toEqual(["1.1.1.1:53", "9.9.9.9:53"]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
});
