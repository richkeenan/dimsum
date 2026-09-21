import { test, expect } from "../../web/e2e";
import { fixtureAPI, settings, activation } from "./fixtures";
test.beforeEach(async ({ page }) => fixtureAPI(page));
test("friendly-name changes are surgical indexed edits", async ({ page }) => {
  let body: unknown;
  await page.route("**/api/v1/clients", (route) => {
    if (route.request().method() === "PATCH") {
      body = route.request().postDataJSON();
      return route.fulfill({ json: activation });
    }
    return route.fulfill({
      json: {
        status: activation,
        items: [{ address: "192.0.2.12", name: "Old name" }],
      },
    });
  });
  await page.goto("/clients");
  await page.getByRole("button", { name: "Edit", exact: true }).click();
  await page.getByLabel("Friendly name", { exact: true }).fill("Study laptop");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  expect(body).toEqual({
    revision: activation.saved_revision,
    edits: [{ path: ["0", "name"], value: "Study laptop" }],
  });
});
test("list toggle shows pending activation and failed refresh preserves configured source", async ({
  page,
}) => {
  let pending = false;
  await page.route("**/api/v1/settings", (route) =>
    route.fulfill({
      json: { ...settings, status: { ...activation, pending } },
    }),
  );
  await page.route("**/api/v1/lists", (route) => {
    if (route.request().method() === "PATCH") {
      pending = true;
      return route.fulfill({ json: { ...activation, pending: true } });
    }
    return route.fulfill({
      json: {
        status: { ...activation, pending },
        items: [
          {
            id: "privacy",
            url: "https://example.test/list",
            dialect: "domains",
            domain_kind: "exact",
            enabled: !pending,
          },
        ],
      },
    });
  });
  await page.route("**/api/v1/jobs", (route) =>
    route.fulfill({
      status: 503,
      json: {
        error: {
          code: "refresh_failed",
          message: "Download failed; previous active version retained",
        },
      },
    }),
  );
  await page.goto("/lists");
  await page.getByRole("button", { name: "Edit", exact: true }).click();
  await page.getByLabel("Enabled", { exact: true }).selectOption("false");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Pending activation")).toBeVisible();
  await page.getByRole("button", { name: "Refresh lists" }).click();
  await expect(
    page.getByText("Download failed; previous active version retained"),
  ).toBeVisible();
  await expect(
    page.getByRole("cell", { name: "privacy", exact: true }),
  ).toBeVisible();
});
test("timed pause sends an absolute expiry and the saved revision", async ({
  page,
}) => {
  let body:
    { revision: string; enabled: boolean; pause_until: string } | undefined;
  await page.route("**/api/v1/blocking", (route) => {
    body = route.request().postDataJSON();
    return route.fulfill({ json: activation });
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Blocking controls" }).click();
  await page.getByLabel("Pause duration").selectOption("30");
  await page
    .getByRole("button", { name: "Pause blocking", exact: true })
    .click();
  await expect.poll(() => body?.enabled).toBe(false);
  expect(body?.revision).toBe(activation.saved_revision);
  expect(Date.parse(body!.pause_until) - Date.now()).toBeGreaterThan(
    29 * 60 * 1000,
  );
});
test("outdated edits stay unsaved and reload does not discard the draft", async ({
  page,
}) => {
  let sent: unknown;
  await page.route("**/api/v1/settings", (route) => {
    if (route.request().method() === "PATCH") {
      sent = route.request().postDataJSON();
      return route.fulfill({
        status: 409,
        json: { code: "revision_conflict", message: "Changed on disk" },
      });
    }
    return route.fulfill({ json: settings });
  });
  await page.goto("/settings");
  await page.getByLabel("Configuration path").fill("cache.bytes");
  await page.getByLabel("New value (JSON)").fill("4194304");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Configuration changed on disk")).toBeVisible();
  expect(sent).toEqual({
    revision: settings.status.saved_revision,
    edits: [{ path: ["cache", "bytes"], value: 4194304 }],
  });
  await page.getByRole("button", { name: "Reload", exact: true }).click();
  await expect(page.getByLabel("New value (JSON)")).toHaveValue("4194304");
  await expect(page.getByText("8388608", { exact: false })).toBeVisible();
});
test("server rejects regex without closing the editor", async ({ page }) => {
  await page.route("**/api/v1/rules", (route) =>
    route.request().method() === "POST"
      ? route.fulfill({
          status: 422,
          json: {
            code: "invalid_rule",
            message: "Regex has an unclosed character class",
          },
        })
      : route.fulfill({ json: { status: activation, items: [] } }),
  );
  await page.goto("/rules");
  await page.getByRole("button", { name: "Add rule" }).click();
  await page.getByLabel("Rule ID").fill("invalid-test");
  await page.getByLabel("Match type").selectOption("regex");
  await page.getByLabel("Pattern", { exact: true }).fill("[");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(
    page.getByText("Regex has an unclosed character class"),
  ).toBeVisible();
  await expect(page.getByRole("dialog")).toBeVisible();
});
test("failed backup reports failure without claiming an export", async ({
  page,
}) => {
  await page.route("**/api/v1/jobs", (route) =>
    route.request().method() === "POST"
      ? route.fulfill({
          status: 503,
          json: {
            code: "backup_failed",
            message: "Backup destination is not writable",
          },
        })
      : route.fulfill({ json: { items: [] } }),
  );
  await page.goto("/jobs");
  await page.getByRole("button", { name: "Start job" }).click();
  await expect(
    page.getByText("Backup destination is not writable"),
  ).toBeVisible();
  await expect(page.getByText("No jobs have been recorded.")).toBeVisible();
});
