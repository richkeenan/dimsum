import { test, expect } from "../../web/e2e";
import { fixtureAPI, activation, policyFixture } from "./fixtures";

test("devices can move between visual profile groups and the table reads back the assignment", async ({
  page,
}) => {
  await fixtureAPI(page);
  // Local HTTP installations do not provide randomUUID.
  await page.addInitScript(() =>
    Object.defineProperty(crypto, "randomUUID", { value: undefined }),
  );
  const clients: any[] = [];
  const observed = {
    address: "192.0.2.20",
    name: "Study tablet",
    authoritative_mac: "02:00:00:00:00:20",
    count: "450",
    blocked: "10",
  };
  const writes: any[] = [];
  await page.route("**/api/v1/clients*", (route) =>
    route.fulfill({
      json: {
        status: activation,
        items: clients,
        observed_available: true,
        observed: { items: [{ ...observed, client_id: clients[0]?.id }] },
      },
    }),
  );
  await page.route("**/api/v1/profiles", (route) =>
    route.fulfill({
      json: { status: activation, items: [{ id: "kids", name: "Kids" }] },
    }),
  );
  await page.route("**/api/v1/client-policy**", (route) => {
    if (route.request().method() === "PATCH") {
      const body = route.request().postDataJSON();
      writes.push(body);
      if (body.create)
        clients.push({ id: body.id, policy_id: body.id, name: body.name });
      clients[0].profile = body.profile;
      return route.fulfill({ json: activation });
    }
    return route.fulfill({
      json: { ...policyFixture, scope: "network", id: "", desired: {} },
    });
  });
  await page.goto("/profiles");
  await page.getByRole("button", { name: "Profiles", exact: true }).click();
  const group = (name: string) =>
    page.getByRole("region", { name: `${name} devices`, exact: true });
  await expect(
    group("Network defaults").getByLabel("Profile for Study tablet"),
  ).toHaveValue("");
  await page.getByRole("searchbox", { name: "Find device" }).fill("STUDY");
  await page.getByTitle("Drag Study tablet to a profile").dragTo(group("Kids"));
  await expect(
    group("Kids").getByLabel("Profile for Study tablet"),
  ).toHaveValue("kids");
  expect(writes[0]).toMatchObject({
    create: true,
    profile: "kids",
    lease_address: "192.0.2.20",
  });
  expect(writes[0]).not.toHaveProperty("selectors");
  await page.getByRole("link", { name: "Devices", exact: true }).click();
  await expect(page.getByLabel("Profile for Study tablet")).toHaveValue("kids");
  await page.getByLabel("Profile for Study tablet").selectOption("");
  await expect.poll(() => writes.length).toBe(2);
  expect(writes[1]).toEqual({
    revision: activation.saved_revision,
    scope: "client",
    id: clients[0].id,
    profile: "",
  });
  await expect(page.getByLabel("Profile for Study tablet")).toHaveValue("");
  await expect(
    page.getByRole("cell", { name: "450", exact: true }),
  ).toBeVisible();
});

test("assignment activation errors survive legacy promotion and clear after active readback", async ({
  page,
}) => {
  await fixtureAPI(page);
  let client: any = {
    policy_id: "address:192.0.2.20",
    address: "192.0.2.20",
    name: "Tablet",
  };
  let status: any = activation;
  await page.route("**/api/v1/clients*", (route) =>
    route.fulfill({
      json: {
        status,
        items: [client],
        observed_available: true,
        observed: {
          items: [
            {
              address: "192.0.2.20",
              client_id: client.policy_id,
              name: "Tablet",
            },
          ],
        },
      },
    }),
  );
  await page.route("**/api/v1/profiles", (route) =>
    route.fulfill({ json: { status, items: [{ id: "kids", name: "Kids" }] } }),
  );
  await page.route("**/api/v1/client-policy", (route) => {
    const body = route.request().postDataJSON();
    expect(body.promote_id).toMatch(/^device-/);
    client = {
      ...client,
      id: body.promote_id,
      policy_id: body.promote_id,
      profile: body.profile,
    };
    status = {
      ...activation,
      saved_revision: "new",
      pending: true,
      error: "Activation unavailable",
    };
    return route.fulfill({ json: status });
  });
  await page.goto("/clients");
  await page.getByLabel("Profile for Tablet").selectOption("kids");
  await expect(page.getByLabel("Profile for Tablet")).toHaveValue("kids");
  await expect(page.getByRole("alert")).toContainText("Activation unavailable");
  status = { ...activation, saved_revision: "new", active_revision: "new" };
  await page.getByRole("button", { name: "Reload displayed data" }).click();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(page.getByText("Saved", { exact: true })).toHaveCount(0);
  await expect(page.getByLabel("Profile for Tablet")).toHaveValue("kids");
});

test("thousands of inherited network rules stay behind one link", async ({
  page,
}) => {
  await fixtureAPI(page);
  await page.route("**/api/v1/client-policy?*", (route) =>
    route.fulfill({
      json: {
        ...policyFixture,
        scope: "network",
        id: "",
        desired: {},
        effective: {
          ...policyFixture.effective,
          rules: Array.from({ length: 2000 }, (_, i) => ({
            source: {},
            rule: {
              id: `rule-${i}`,
              pattern: `ads-${i}.example.test`,
              enabled: true,
              action: "deny",
              kind: "exact",
            },
          })),
        },
      },
    }),
  );
  await page.goto("/profiles");
  await expect(
    page.getByRole("link", { name: "View network rules" }),
  ).toHaveAttribute("href", "/rules");
  await expect(page.getByText(/2000 network rules/)).toBeVisible();
  await expect(
    page.getByText("ads-1999.example.test", { exact: false }),
  ).toHaveCount(0);
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(width);
    await expect(page.getByLabel("DNS filtering")).toHaveValue("on");
  }
});
