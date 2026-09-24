import { test, expect } from "../../web/e2e";
import { fixtureAPI, activation } from "./fixtures";

test("shared built-in list editor saves, restores and resets at desktop and mobile sizes", async ({
  page,
}) => {
  await fixtureAPI(page);
  let revision = 1;
  let removed = false;
  let custom: string[] = [];
  const status = () => ({
    ...activation,
    saved_revision: `r${revision}`,
    active_revision: `r${revision}`,
  });
  await page.route("**/api/v1/lists", (route) =>
    route.fulfill({
      json: {
        status: status(),
        items: [
          {
            id: "work",
            url: "builtin://work-compatibility",
            dialect: "dns-adblock",
            domain_kind: "suffix",
            enabled: true,
            default_apply: false,
          },
        ],
      },
    }),
  );
  await page.route("**/api/v1/catalog", (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: "work",
            label: "Work tools compatibility",
            category: "compatibility",
            url: "builtin://work-compatibility",
            available: true,
            dialect: "dns-adblock",
            domain_kind: "suffix",
          },
        ],
      },
    }),
  );
  await page.route("**/api/v1/builtin-lists/work", (route) => {
    if (route.request().method() === "PATCH") {
      const body = route.request().postDataJSON();
      expect(body.revision).toBe(`r${revision}`);
      if (body.action === "add") custom.push(body.domain);
      if (body.action === "remove") removed = true;
      if (body.action === "restore") removed = false;
      if (body.action === "reset") {
        removed = false;
        custom = [];
      }
      revision++;
      return route.fulfill({ json: status() });
    }
    return route.fulfill({
      json: {
        id: "work",
        revision: `r${revision}`,
        status: status(),
        customized: removed || custom.length > 0,
        entries: [
          ...Array.from({ length: 125 }, (_, i) => ({
            domain: `analytics-${i}.example`,
            origin: "builtin",
            removed: i === 0 && removed,
          })),
          ...custom.map((domain) => ({ domain, origin: "custom", removed: false })),
        ],
      },
    });
  });
  await page.goto("/lists?edit=work");
  const dialog = page.getByRole("dialog", { name: "Work tools compatibility" });
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: "Remove analytics-0.example", exact: true }).click();
  await expect(
    dialog.getByRole("button", { name: "Restore analytics-0.example", exact: true }),
  ).toBeEnabled();
  await dialog.getByRole("button", { name: "Restore analytics-0.example", exact: true }).click();
  await dialog.getByLabel("Domain to allow").fill("metrics.example");
  await dialog.getByRole("button", { name: "Add domain", exact: true }).click();
  await expect(dialog.getByLabel("Domain to allow")).toHaveValue("");
  await dialog.getByLabel("Search domains").fill("metrics");
  await expect(dialog.getByText("metrics.example", { exact: true })).toBeVisible();
  await dialog.getByLabel("Search domains").fill("");
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 844 });
    const box = await dialog.boundingBox();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(width + 1);
    await expect(dialog.getByRole("button", { name: "Done", exact: true })).toBeInViewport();
    await expect(dialog.getByLabel("Search domains")).toBeInViewport();
    await page.screenshot({ path: `test-results/builtin-list-${width}.png` });
  }
  await dialog.getByRole("button", { name: "Reset to shipped defaults" }).click();
  await dialog.getByRole("button", { name: "Confirm reset" }).click();
  await expect(dialog.getByText("125 allowed domains · Shipped defaults")).toBeVisible();
  await dialog.getByRole("button", { name: "Done", exact: true }).click();
  await expect(dialog).not.toBeVisible();
  await page.getByRole("button", { name: /Compatibility.*used by default/ }).click();
  await page.getByRole("button", { name: "Edit domains" }).click();
  await expect(dialog).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("button", { name: "Edit domains" })).toBeFocused();
});
