import { test, expect } from "../../web/e2e";
import { fixtureAPI, activation } from "./fixtures";

test("device policy: atomic list, sparse reset, profile, relink and narrow-screen explanation", async ({
  page,
}, testInfo) => {
  await fixtureAPI(page);
  const writes: any[] = [];
  let revision = 1;
  let client: any = {
    policy_id: "tablet",
    id: "tablet",
    name: "Study tablet",
    selectors: { macs: ["02:00:00:00:00:01"] },
    overrides: {},
  };
  const sources: any[] = [];
  const status = () => ({
    ...activation,
    saved_revision: `r${revision}`,
    active_revision: `r${revision}`,
    sources,
  });
  const effective = (target = client) => ({
    id: "tablet",
    profile_id: target.profile ?? "",
    blocking: {
      value: target.overrides.blocking ?? true,
      source:
        target.overrides.blocking === undefined
          ? {}
          : { kind: "client", id: "tablet" },
    },
    filtering: target.overrides.blocking ?? true,
    paused_until: "0001-01-01T00:00:00Z",
    global_paused: false,
    lists: Object.fromEntries(
      sources.map((s) => [
        s.id,
        {
          value: target.overrides.lists?.[s.id] ?? false,
          source: { kind: "client", id: "tablet" },
        },
      ]),
    ),
    upstream_source: {},
    route_id: "route",
    upstream: { upstreams: ["192.0.2.53:53"] },
    override_count: Object.keys(target.overrides).length,
    rules: [],
  });
  await page.route("**/api/v1/clients?*", (route) =>
    route.fulfill({
      json: {
        status: status(),
        items: [client],
        observed_available: true,
        observed: {
          items: [
            {
              address: "192.0.2.20",
              name: "Study tablet",
              client_id: "tablet",
              matching_method: "dhcp_mac",
              authoritative_mac: "02:00:00:00:00:01",
            },
          ],
        },
      },
    }),
  );
  await page.route("**/api/v1/clients", (route) =>
    route.fulfill({
      json: {
        status: status(),
        items: [client],
        observed_available: true,
        observed: {
          items: [
            {
              address: "192.0.2.20",
              client_id: "tablet",
              matching_method: "dhcp_mac",
              authoritative_mac: "02:00:00:00:00:01",
            },
          ],
        },
      },
    }),
  );
  await page.route("**/api/v1/profiles", (route) =>
    route.fulfill({
      json: { status: status(), items: [{ id: "children", name: "Children" }] },
    }),
  );
  await page.route("**/api/v1/client-policy**", async (route) => {
    if (route.request().method() === "GET")
      return route.fulfill({
        json: {
          status: status(),
          scope: "client",
          id: "tablet",
          desired: client,
          effective: effective(),
          active: effective(),
        },
      });
    const body = route.request().postDataJSON();
    if (route.request().url().endsWith("/preview"))
      return route.fulfill({
        json: {
          revision: body.revision,
          id: "tablet",
          effective: effective(),
          changed_clients: ["tablet"],
          network_changed: false,
          downloads_pending: body.subscribe?.map((s: any) => s.id) ?? [],
        },
      });
    writes.push(body);
    if (body.profile !== undefined) client.profile = body.profile;
    if (body.selectors) client.selectors = body.selectors;
    if (body.lease_address) client.selectors = { macs: ["02:00:00:00:00:01"] };
    for (const sub of body.subscribe ?? [])
      sources.push({
        id: sub.id,
        enabled: true,
        usable: false,
        rules: 0,
        error: "Download failed",
      });
    for (const field of body.fields ?? []) {
      if (field.path[0] === "lists") {
        client.overrides.lists ??= {};
        if (field.reset) delete client.overrides.lists[field.path[1]];
        else client.overrides.lists[field.path[1]] = field.value;
      } else if (field.reset) delete client.overrides[field.path[0]];
      else client.overrides[field.path[0]] = field.value;
    }
    revision++;
    await route.fulfill({ json: status() });
  });
  await page.route("**/api/v1/rules/test", (route) =>
    route.fulfill({
      json: {
        name: "ads.example.test",
        normalized: "ads.example.test",
        generation: "42",
        client_id: "tablet",
        matching_method: "configured_id",
        handling: "policy",
        effective: effective(),
        decision: {
          result: "block",
          rule_id: "rule-1",
          scope: { kind: "client", id: "tablet" },
          source_ids: ["custom"],
        },
      },
    }),
  );
  await page.goto("/clients");
  await expect(
    page.getByText("Authoritative DHCP MAC", { exact: false }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Study tablet", exact: true }).click();
  await expect(page).toHaveURL(/device=tablet/);
  await page.getByLabel("Blocking", { exact: true }).selectOption("on");
  await page.getByLabel("Changes only").check();
  await expect(page.getByLabel("Primary servers")).toHaveCount(0);
  await page
    .getByRole("button", { name: "Save 1 change", exact: true })
    .click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0].fields).toEqual([{ path: ["blocking"], value: true }]);
  await page.getByRole("button", { name: "Reset Blocking" }).click();
  await page
    .getByRole("button", { name: "Save 1 change", exact: true })
    .click();
  await expect.poll(() => writes.length).toBe(2);
  expect(writes[1].fields).toEqual([{ path: ["blocking"], reset: true }]);
  await page.getByLabel("Profile", { exact: true }).selectOption("children");
  await page
    .getByRole("button", { name: "Save 1 change", exact: true })
    .click();
  await expect.poll(() => writes.length).toBe(3);
  expect(writes[2].profile).toBe("children");
  await page.getByText("Add an available list", { exact: true }).click();
  await page
    .getByRole("button", { name: "Add Fixture privacy", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Save 1 change", exact: true })
    .click();
  await expect.poll(() => writes.length).toBe(4);
  expect(writes[3]).toMatchObject({
    scope: "client",
    id: "tablet",
    subscribe: [{ id: "privacy", enabled: true }],
    fields: [{ path: ["lists", "privacy"], value: true }],
  });
  await expect(page.getByText(/privacy: Source not active/)).toBeVisible();
  await page.getByText("Matching identity · tablet", { exact: true }).click();
  await page
    .getByLabel("Relink to authoritative DHCP lease")
    .selectOption("192.0.2.20");
  await page.getByRole("button", { name: "Use lease MAC only" }).click();
  await page
    .getByRole("button", { name: "Save 1 change", exact: true })
    .click();
  await expect.poll(() => writes.length).toBe(5);
  expect(writes[4].lease_address).toBe("192.0.2.20");
  expect(writes[4].selectors).toBeUndefined();
  await page.getByLabel("Domain", { exact: true }).fill("ads.example.test");
  await page
    .getByRole("button", { name: "Explain domain", exact: true })
    .click();
  await expect(page.getByText(/Winning rule: rule-1/)).toBeVisible();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({
    path: testInfo.outputPath("device-policy-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect(page.getByLabel("Blocking", { exact: true })).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("device-policy-mobile.png"),
    fullPage: false,
  });
  await page.getByLabel("Name", { exact: true }).focus();
  await page.keyboard.press("End");
  await page.keyboard.type(" test");
  await expect(
    page.getByRole("button", { name: "Save 1 change", exact: true }),
  ).toBeVisible();
  await page.getByLabel("Changes only").focus();
  await page.keyboard.press("Space");
  await expect(page.getByLabel("Primary servers", { exact: true })).toHaveCount(
    0,
  );
  await page.getByRole('button', { name: 'Open navigation', exact: true }).click();
  await page.getByRole('button', { name: 'Dark appearance', exact: true }).click();
  await page.getByRole('button', { name: 'Close navigation', exact: true }).last().click();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({
    path: testInfo.outputPath("device-policy-mobile-dark-changes.png"),
    fullPage: false,
  });
});
