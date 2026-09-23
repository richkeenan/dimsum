import { test, expect } from "../../web/e2e";
import { activation, fixtureAPI, query, policyFixture } from "./fixtures";

test.beforeEach(async ({ page }) => {
  await fixtureAPI(page);
  await page.route("**/api/v1/rules/test", (route) =>
    route.fulfill({
      json: { client_id: "study-laptop", decision: { result: "block" } },
    }),
  );
});

test("local records explain their precedence instead of offering ineffective blocking", async ({
  page,
}) => {
  const local = { ...query, outcome: "local" };
  await page.route("**/api/v1/queries?**", (route) =>
    route.fulfill({ json: { items: [local], complete: true } }),
  );
  await page.route("**/api/v1/queries/" + query.id, (route) =>
    route.fulfill({ json: local }),
  );
  await page.goto("/queries");
  await expect(
    page.getByRole("button", { name: query.name, exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Block " + query.name }),
  ).not.toBeVisible();
  await page.getByRole("button", { name: query.name, exact: true }).click();
  const panel = page.getByRole("dialog", { name: "Query detail" });
  await expect(
    panel.getByText(/Local DNS records take priority/),
  ).toBeVisible();
  await expect(
    panel.getByRole("button", { name: "Create block rule" }),
  ).not.toBeVisible();
});

test("blocking after an allow exception explains that the exception still wins", async ({
  page,
}) => {
  let outcome = "blocked";
  await page.route("**/api/v1/queries?**", (route) =>
    route.fulfill({ json: { items: [{ ...query, outcome }], complete: true } }),
  );
  await page.route("**/api/v1/rules/test", (route) =>
    route.fulfill({
      json: {
        client_id: "study-laptop",
        decision: { result: "allow", rule_id: "existing-allow" },
      },
    }),
  );
  const actions: string[] = [];
  await page.route("**/api/v1/client-policy", (route) => {
    actions.push(route.request().postDataJSON().fields[0].value[0].action);
    return route.fulfill({ json: { status: activation } });
  });
  await page.goto("/queries");
  await page
    .getByRole("button", { name: "Allow " + query.name, exact: true })
    .click();
  await page
    .getByRole("button", { name: "Create allow rule", exact: true })
    .click();
  await expect.poll(() => actions).toEqual(["allow"]);
  await expect(
    page.getByRole("button", { name: "Create allow rule", exact: true }),
  ).toBeDisabled();
  outcome = "forwarded";
  await page.reload();
  await page
    .getByRole("button", { name: "Block " + query.name, exact: true })
    .click();
  await page
    .getByRole("button", { name: "Create block rule", exact: true })
    .click();
  await expect(
    page.getByText(/an allow exception still takes precedence/),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Edit custom rules" }),
  ).toHaveAttribute("href", "/rules");
  await expect(
    page.getByText("Block rule active", { exact: true }),
  ).not.toBeVisible();
  expect(actions).toEqual(["allow", "deny"]);
});

const answered = {
  ...query,
  outcome: "forwarded",
  duration_us: "11182",
  rcode: 0,
  response: {
    truncated: false,
    records: [
      {
        name: query.name,
        type: "CNAME",
        value: "edge.example.test",
        ttl: 300,
        section: "answer",
      },
      {
        name: "edge.example.test",
        type: "A",
        value: "192.0.2.8",
        ttl: 42,
        section: "answer",
      },
      {
        name: "edge.example.test",
        type: "A",
        value: "192.0.2.9",
        ttl: 0,
        section: "answer",
      },
    ],
  },
};

test("broader rule scopes require an explicit choice and profile writes stay sparse", async ({
  page,
}) => {
  await page.route("**/api/v1/client-policy?scope=profile&*", (route) =>
    route.fulfill({
      json: {
        ...policyFixture,
        scope: "profile",
        id: "children",
        desired: { id: "children", policy: {} },
      },
    }),
  );
  await page.route("**/api/v1/profiles", (route) =>
    route.fulfill({
      json: {
        status: activation,
        items: [{ id: "children", name: "Children" }],
      },
    }),
  );
  const network: unknown[] = [],
    profile: any[] = [];
  await page.route("**/api/v1/rules", (route) => {
    network.push(route.request().postDataJSON());
    return route.fulfill({ json: activation });
  });
  await page.route("**/api/v1/client-policy", (route) => {
    profile.push(route.request().postDataJSON());
    return route.fulfill({ json: activation });
  });
  await page.goto("/queries");
  await page
    .getByRole("button", { name: `Allow ${query.name}`, exact: true })
    .click();
  const target = page.getByLabel(`Rule target for ${query.client}`, {
    exact: true,
  });
  await expect(target).toHaveValue("device");
  await target.selectOption("network");
  await page
    .getByRole("button", { name: "Create allow rule", exact: true })
    .click();
  await expect.poll(() => network.length).toBe(1);
  expect(profile).toHaveLength(0);
  await page.reload();
  await page
    .getByRole("button", { name: `Allow ${query.name}`, exact: true })
    .click();
  await target.selectOption("profile:children");
  await page
    .getByRole("button", { name: "Create allow rule", exact: true })
    .click();
  await expect.poll(() => profile.length).toBe(1);
  expect(profile[0]).toMatchObject({
    scope: "profile",
    id: "children",
    fields: [
      { path: ["rules"], value: [{ pattern: query.name, action: "allow" }] },
    ],
  });
  expect(network).toHaveLength(1);
});

test("answers, historical TTLs and contextual blocking are useful without technical columns", async ({
  page,
}, testInfo) => {
  await page.route("**/api/v1/queries?**", (route) =>
    route.fulfill({ json: { items: [answered], complete: true } }),
  );
  await page.route("**/api/v1/queries/" + query.id, (route) =>
    route.fulfill({ json: answered }),
  );
  let saved: unknown;
  await page.route("**/api/v1/client-policy", (route) => {
    saved = route.request().postDataJSON();
    return route.fulfill({ json: { status: activation } });
  });
  await page.goto("/queries");
  await expect(page.getByRole("cell", { name: /192\.0\.2\.8/ })).toBeVisible();
  await expect(
    page.getByRole("cell", { name: "Forwarded 11.2 ms" }),
  ).toBeVisible();
  await page.screenshot({
    path: testInfo.outputPath("query-table.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: query.name, exact: true }).click();
  const panel = page.getByRole("dialog", { name: "Query detail" });
  await expect(panel.getByText("192.0.2.9", { exact: true })).toBeVisible();
  await expect(panel.getByTitle("TTL when answered").first()).toBeVisible();
  await expect(panel.getByText("42 s", { exact: true })).toBeVisible();
  await expect(panel.getByText("0 s", { exact: true })).toBeVisible();
  await expect(panel.getByText("NOERROR", { exact: false })).toBeVisible();
  await expect(
    panel.getByRole("combobox", { name: "Action", exact: true }),
  ).toHaveValue("deny");
  await page.screenshot({
    path: testInfo.outputPath("query-detail.png"),
    fullPage: true,
  });
  await page.keyboard.press("Escape");
  await page
    .getByRole("button", { name: "Block " + query.name, exact: true })
    .click();
  await page
    .getByRole("button", { name: "Create block rule", exact: true })
    .click();
  await expect
    .poll(() => saved)
    .toMatchObject({
      revision: activation.saved_revision,
      scope: "client",
      id: "study-laptop",
      fields: [
        {
          path: ["rules"],
          value: [{ action: "deny", kind: "exact", pattern: query.name }],
        },
      ],
    });
  await expect(
    page.getByRole("button", { name: "Create block rule", exact: true }),
  ).toBeDisabled();
  await expect(page.getByRole("dialog")).toBeVisible();
});

test("inline failures can be retried and pending rules are not labelled active", async ({
  page,
}) => {
  let attempts = 0;
  await page.route("**/api/v1/client-policy", (route) => {
    attempts++;
    return route.fulfill(
      attempts === 1
        ? {
            status: 409,
            json: { code: "conflict", message: "Revision changed" },
          }
        : {
            json: {
              status: { ...activation, pending: true, active_revision: "old" },
            },
          },
    );
  });
  await page.goto("/queries");
  const allow = page.getByRole("button", {
    name: "Allow " + query.name,
    exact: true,
  });
  await allow.click();
  const save = page.getByRole("button", {
    name: "Create allow rule",
    exact: true,
  });
  await save.click();
  await expect(page.getByRole("alert")).toContainText("Settings changed");
  await save.click();
  await expect(
    page.getByText("Allow rule saved · pending", { exact: true }),
  ).toBeVisible();
  await expect(save).toBeDisabled();
  expect(attempts).toBe(2);
});

test("inline blocking works when randomUUID is unavailable", async ({
  page,
}) => {
  await page.addInitScript(() => {
    Object.defineProperty(crypto, "randomUUID", { value: undefined });
  });
  await page.route("**/api/v1/queries?**", (route) =>
    route.fulfill({
      json: { items: [{ ...query, outcome: "forwarded" }], complete: true },
    }),
  );
  let item: { id?: string } | undefined;
  await page.route("**/api/v1/client-policy", async (route) => {
    item = JSON.parse(route.request().postData() ?? "{}").fields[0].value[0];
    await route.fulfill({ json: { status: activation } });
  });
  await page.goto("/queries");
  await page.getByRole("button", { name: "Block " + query.name }).click();
  await page
    .getByRole("button", { name: "Create block rule", exact: true })
    .click();
  await expect(page.getByRole("alert")).not.toBeVisible();
  await expect.poll(() => item?.id).toMatch(/^query-[0-9a-f]{24}$/);
  await expect(
    page.getByRole("button", { name: "Create block rule", exact: true }),
  ).toBeDisabled();
});

test("long query details can be scrolled to and operate their final action", async ({
  page,
}, testInfo) => {
  await page.route("**/api/v1/queries/" + query.id, (route) =>
    route.fulfill({
      json: {
        ...query,
        rule_description_available: true,
        rule_description:
          "id: fixture-rule\nkind: exact\nclass: subscription-deny\npattern: telemetry.example.com\nsource: fixture-list",
        source_id: "fixture-list",
        response: {
          truncated: true,
          records: Array.from({ length: 16 }, (_, index) => ({
            name: query.name,
            type: "AAAA",
            value: `2001:db8:1234:5678:abcd:1234:5678:${index + 1}`,
            ttl: 60,
            section: "answer",
          })),
        },
      },
    }),
  );
  let saved: unknown;
  await page.route("**/api/v1/client-policy", (route) => {
    saved = route.request().postDataJSON();
    return route.fulfill({ json: { status: activation } });
  });
  await page.goto("/queries");
  await page.getByRole("button", { name: "Dark appearance" }).click();
  await page.setViewportSize({ width: 390, height: 640 });
  await page.getByRole("button", { name: query.name, exact: true }).click();
  const panel = page.getByRole("dialog", { name: "Query detail" });
  await expect(panel).toBeVisible();
  await panel.hover();
  await page.mouse.wheel(0, 10000);
  const save = panel.getByRole("button", { name: "Create allow rule" });
  await expect(save).toBeInViewport();
  expect(await panel.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(
    true,
  );
  await page.screenshot({
    path: testInfo.outputPath("query-detail-mobile-dark-bottom.png"),
    fullPage: true,
  });
  // A clipped grid child can be reported in the viewport but cannot be clicked.
  await save.click({ timeout: 3000 });
  await expect
    .poll(() => saved)
    .toMatchObject({
      revision: activation.saved_revision,
      scope: "client",
      id: "study-laptop",
      fields: [
        {
          path: ["rules"],
          value: [{ action: "allow", kind: "exact", pattern: query.name }],
        },
      ],
    });
  await page.keyboard.press("Escape");
  await expect(
    page.getByRole("button", { name: query.name, exact: true }),
  ).toBeFocused();
});
