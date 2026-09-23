import { test, expect } from "../../web/e2e";
import { fixtureAPI, activation, policyFixture } from "./fixtures";

test("long policy content fits mobile and removal remains accessible", async ({
  page,
}) => {
  await fixtureAPI(page);
  const pattern =
    Array(4).fill("tracking-with-a-long-but-valid-label").join(".") +
    ".example.test";
  const rule = {
    id: "long-rule",
    enabled: true,
    pattern,
    kind: "exact",
    action: "deny",
  };
  await page.route("**/api/v1/client-policy?*", (route) =>
    route.fulfill({
      json: {
        ...policyFixture,
        desired: { ...policyFixture.desired, overrides: { rules: [rule] } },
      },
    }),
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/clients?device=study-laptop");
  const remove = page.getByRole("button", {
    name: `Remove ${pattern}`,
    exact: true,
  });
  await expect(remove).toBeVisible();
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth),
  ).toBeLessThanOrEqual(390);
  await expect(remove).toHaveText("Remove");
  await remove.click();
  await expect(remove).toHaveCount(0);
});

test("device drafts survive cancelled navigation and warn on browser unload", async ({
  page,
}) => {
  await fixtureAPI(page);
  await page.goto("/clients?device=study-laptop");
  await page.getByLabel("Name", { exact: true }).fill("Draft laptop");
  page.once("dialog", (dialog) => dialog.dismiss());
  await page.getByRole("button", { name: "Back to devices" }).click();
  await expect(page.getByLabel("Name", { exact: true })).toHaveValue(
    "Draft laptop",
  );
  const reloadDialog = page.waitForEvent("dialog");
  // A dismissed beforeunload cancels navigation; Playwright may keep waiting
  // for the load event that will never arrive.
  const reload = page.reload({ timeout: 2000 }).catch(() => {});
  const dialog = await reloadDialog;
  expect(dialog.type()).toBe("beforeunload");
  await dialog.dismiss();
  await reload;
  await expect(page.getByLabel("Name", { exact: true })).toHaveValue(
    "Draft laptop",
  );
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Back to devices" }).click();
  await expect(
    page.getByRole("button", { name: "Add device", exact: true }),
  ).toBeVisible();
});

test("profile switching protects drafts and create form manages focus", async ({
  page,
}) => {
  await fixtureAPI(page);
  await page.route("**/api/v1/profiles", (route) =>
    route.fulfill({
      json: { status: activation, items: [{ id: "family", name: "Family" }] },
    }),
  );
  await page.route("**/api/v1/client-policy?*", (route) => {
    const profile =
      new URL(route.request().url()).searchParams.get("scope") === "profile";
    return route.fulfill({
      json: {
        ...policyFixture,
        scope: profile ? "profile" : "network",
        id: profile ? "family" : "",
        desired: profile ? { id: "family", name: "Family", policy: {} } : {},
      },
    });
  });
  await page.goto("/profiles");
  await page.getByLabel("Blocking", { exact: true }).selectOption("off");
  page.once("dialog", (dialog) => dialog.dismiss());
  await page.getByLabel("Policy to edit").selectOption("family");
  await expect(page.getByLabel("Policy to edit")).toHaveValue("");
  await expect(page.getByLabel("Blocking", { exact: true })).toHaveValue("off");
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByLabel("Policy to edit").selectOption("family");
  await expect(
    page.getByRole("heading", { name: "Profile: Family" }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Create profile", exact: true })
    .click();
  await expect(page.getByLabel("Stable ID", { exact: true })).toBeFocused();
  await page.getByLabel("Stable ID", { exact: true }).fill("new-profile");
  await expect(page.getByLabel("Blocking", { exact: true })).toBeDisabled();
  page.once("dialog", (dialog) => dialog.dismiss());
  await page.getByLabel("Policy to edit").selectOption("");
  await expect(page.getByLabel("Stable ID", { exact: true })).toHaveValue(
    "new-profile",
  );
  await expect(page.getByLabel("Policy to edit")).toHaveValue("family");
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Create profile", exact: true }),
  ).toBeFocused();
});

test("changes-only shows pending pause and matching identity", async ({
  page,
}) => {
  await fixtureAPI(page);
  await page.goto("/clients?device=study-laptop");
  await page.getByRole("button", { name: "Pause 30 min", exact: true }).click();
  await expect(page.getByText(/Pending pause until/)).toBeVisible();
  await page
    .getByText("Matching identity · study-laptop", { exact: true })
    .click();
  await page.getByLabel("Addresses", { exact: true }).fill("192.0.2.44");
  await page
    .getByRole("button", { name: "Stage selector replacement" })
    .click();
  await page.getByLabel("Changes only").check();
  await expect(page.getByText(/Pending pause until/)).toBeVisible();
  await expect(
    page.getByText("Addresses: 192.0.2.44", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Resume device", exact: true })
    .click();
  await expect(
    page.getByText("Pending: resume device on save.", { exact: true }),
  ).toBeVisible();
});

test("new device drafts survive cancelled navigation", async ({ page }) => {
  await fixtureAPI(page);
  await page.goto("/clients");
  await page.getByRole("button", { name: "Add device", exact: true }).click();
  await page.getByLabel("Stable ID", { exact: true }).fill("new-tablet");
  await page.getByLabel("Name", { exact: true }).fill("New tablet");
  page.once("dialog", (dialog) => dialog.dismiss());
  await page.getByRole("button", { name: "Study laptop", exact: true }).click();
  await expect(page.getByLabel("Stable ID", { exact: true })).toHaveValue(
    "new-tablet",
  );
  page.once("dialog", (dialog) => dialog.dismiss());
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(page.getByLabel("Name", { exact: true })).toHaveValue(
    "New tablet",
  );
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Add device", exact: true }),
  ).toBeFocused();
});

test("successful saves and explicit discard clear navigation protection", async ({
  page,
}) => {
  await fixtureAPI(page);
  let prompts = 0;
  page.on("dialog", (dialog) => {
    prompts++;
    void dialog.dismiss();
  });
  await page.goto("/clients?device=study-laptop");
  await page.getByLabel("Name", { exact: true }).fill("Renamed laptop");
  await page
    .getByRole("button", { name: "Save 1 change", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Save 0 changes", exact: true }),
  ).toBeDisabled();
  await page.getByLabel("Name", { exact: true }).fill("Discard this name");
  await page
    .getByRole("button", { name: "Discard staged changes", exact: true })
    .click();
  await page.getByRole("button", { name: "Back to devices" }).click();
  await expect(
    page.getByRole("button", { name: "Add device", exact: true }),
  ).toBeVisible();
  expect(prompts).toBe(0);
});
