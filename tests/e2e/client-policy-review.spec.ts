import { test, expect } from "../../web/e2e";
import { fixtureAPI, activation, policyFixture, query } from "./fixtures";

test("large inventory uses one compact response per refresh and no owner policy requests", async ({
  page,
}) => {
  await fixtureAPI(page);
  const items = Array.from({ length: 512 }, (_, i) => ({
    id: `device-${i}`,
    policy_id: `device-${i}`,
    name: `Device ${i}`,
    ...(i === 0 ? { selectors: { addresses: ["192.0.2.55"] } } : {}),
  }));
  const compact = {
    profile_id: "shared",
    override_count: 2,
    blocking: true,
    filtering: true,
    global_paused: false,
    source_unavailable: false,
  };
  let inventories = 0,
    details = 0;
  await page.route("**/api/v1/clients?*", (route) => {
    inventories++;
    return route.fulfill({
      json: {
        status: activation,
        items,
        observed_available: false,
        policy_summaries: Object.fromEntries(
          items.map((c) => [c.id, { desired: compact, active: compact }]),
        ),
      },
    });
  });
  await page.route("**/api/v1/client-policy?*", (route) => {
    details++;
    return route.fulfill({ json: policyFixture });
  });
  await page.goto("/clients");
  await expect(page.getByText("Device 511", { exact: true })).toBeAttached();
  await expect(page.getByText("Custom settings", { exact: true })).toHaveCount(
    512,
  );
  await page.waitForTimeout(5500); // Include the inventory polling interval in the request budget.
  expect(details).toBe(0);
  expect(inventories).toBeLessThanOrEqual(3);
  await expect(
    page.getByRole("button", {
      name: "View queries for Device 511",
      exact: true,
    }),
  ).toHaveCount(0);
  await page
    .getByRole("button", { name: "View queries for Device 0", exact: true })
    .click();
  await expect(page).toHaveURL(/client=192\.0\.2\.55/);
});

for (const available of [true, false])
  test(`query scope resolves authoritative identity despite ${available ? "truncated" : "unavailable"} observations`, async ({
    page,
  }) => {
    await fixtureAPI(page);
    let saved: any;
    const addresses: string[] = [];
    await page.route("**/api/v1/clients*", (route) =>
      route.fulfill({
        json: {
          status: activation,
          items: [],
          observed_available: available,
          ...(available ? { observed: { items: [], truncated: true } } : {}),
        },
      }),
    );
    await page.route("**/api/v1/rules/test", (route) => {
      const body = route.request().postDataJSON();
      if (body.address) addresses.push(body.address);
      return route.fulfill({
        json: {
          ...policyFixture,
          client_id: "study-laptop",
          matching_method: "address",
          decision: { result: "allow" },
        },
      });
    });
    await page.route("**/api/v1/client-policy", (route) => {
      saved = route.request().postDataJSON();
      return route.fulfill({ json: activation });
    });
    await page.goto(
      "/queries?range=custom&from=2026-09-01T00%3A00%3A00Z&to=2026-09-02T00%3A00%3A00Z",
    );
    await page
      .getByRole("button", { name: `Allow ${query.name}`, exact: true })
      .click();
    await expect(
      page.getByRole("link", { name: "Device settings" }),
    ).toHaveAttribute("href", "/clients?device=study-laptop");
    await page
      .getByRole("button", { name: "Create allow rule", exact: true })
      .click();
    await expect.poll(() => saved?.id).toBe("study-laptop");
    expect(saved.scope).toBe("client");
    expect(addresses).toContain(query.client);
  });

test("route reset restores inherited controls and failed readback preserves the successful write", async ({
  page,
}) => {
  await fixtureAPI(page);
  let writes = 0,
    recover = false;
  const saved = {
    ...activation,
    saved_revision: "r2",
    pending: true,
    error: "Activation failed in fixture",
  };
  await page.route("**/api/v1/client-policy**", (route) => {
    if (route.request().method() === "PATCH") {
      writes++;
      return route.fulfill({ json: saved });
    }
    if (route.request().method() === "POST")
      return route.fulfill({
        json: {
          effective: policyFixture.effective,
          changed_clients: ["study-laptop"],
          network_changed: false,
          downloads_pending: [],
        },
      });
    if (writes && !recover)
      return route.fulfill({
        status: 503,
        json: { error: { message: "Readback unavailable" } },
      });
    return route.fulfill({
      json: { ...policyFixture, status: writes ? saved : activation },
    });
  });
  await page.goto("/clients?device=study-laptop");
  await page.getByText("Advanced: upstream servers", { exact: true }).click();
  await page
    .getByLabel("Primary servers", { exact: true })
    .fill("192.0.2.99:53");
  await page
    .getByLabel("Fallback servers", { exact: true })
    .fill("192.0.2.98:53");
  await page
    .getByRole("button", { name: "Reset upstream", exact: true })
    .click();
  await expect(page.getByLabel("Primary servers", { exact: true })).toHaveValue(
    "192.0.2.53:53",
  );
  await expect(
    page.getByLabel("Fallback servers", { exact: true }),
  ).toHaveValue("");
  await expect(
    page.getByRole("button", { name: "Save 0 changes", exact: true }),
  ).toBeDisabled();
  await page.getByLabel("DNS filtering", { exact: true }).selectOption("off");
  await page
    .getByRole("button", { name: "Save 1 change", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Retry saved policy read", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("Saved · activation failed: Activation failed in fixture", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(page.getByLabel("DNS filtering", { exact: true })).toHaveValue(
    "off",
  );
  await expect(
    page.getByRole("button", { name: "Save 1 change", exact: true }),
  ).toBeDisabled();
  recover = true;
  await page
    .getByRole("button", { name: "Retry saved policy read", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Retry saved policy read", exact: true }),
  ).toHaveCount(0);
  expect(writes).toBe(1);
});

test("reset-all exposes pending effects while retaining setting controls", async ({
  page,
}) => {
  await fixtureAPI(page);
  await page.route("**/api/v1/client-policy**", (route) =>
    route.fulfill({
      json:
        route.request().method() === "GET"
          ? {
              ...policyFixture,
              desired: {
                ...policyFixture.desired,
                paused_until: "2026-09-23T18:00:00Z",
                overrides: {
                  upstream: { upstreams: ["192.0.2.99:53"] },
                  rules: [
                    {
                      id: "own",
                      kind: "exact",
                      action: "deny",
                      pattern: "blocked.example",
                      enabled: true,
                    },
                  ],
                },
              },
            }
          : {
              effective: policyFixture.effective,
              changed_clients: ["study-laptop"],
              network_changed: false,
              downloads_pending: [],
            },
    }),
  );
  await page.goto("/clients?device=study-laptop");
  await page
    .getByRole("button", { name: "Reset all overrides", exact: true })
    .click();
  await page.getByText("Advanced: upstream servers", { exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Upstream servers", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Custom rules", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Device pause", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("Removing owner rules: blocked.example", { exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("DNS filtering", { exact: true })).toHaveValue(
    "inherit",
  );
  await expect(
    page.getByRole("heading", { name: "Filter lists", exact: true }),
  ).toBeVisible();
});
