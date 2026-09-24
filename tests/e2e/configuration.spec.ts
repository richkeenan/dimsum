import { test, expect } from "../../web/e2e";
import { fixtureAPI, settings, activation, policyFixture } from "./fixtures";
test.beforeEach(async ({ page }) => fixtureAPI(page));
test("friendly-name changes preserve the stable policy identity", async ({
  page,
}) => {
  let body: unknown;
  await page.route("**/api/v1/client-policy*", (route) => {
    if (route.request().method() === "PATCH") {
      body = route.request().postDataJSON();
      return route.fulfill({ json: activation });
    }
    return route.fulfill({
      json: {
        ...policyFixture,
        desired: { ...policyFixture.desired, name: "Old name" },
      },
    });
  });
  await page.goto("/clients?device=study-laptop");
  await page.getByLabel("Name", { exact: true }).fill("Study laptop");
  await page
    .getByRole("button", { name: "Save 1 change", exact: true })
    .click();
  await expect
    .poll(() => body)
    .toEqual({
      revision: activation.saved_revision,
      scope: "client",
      id: "study-laptop",
      name: "Study laptop",
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
  await page.getByRole("button", { name: /Ads & Trackers/ }).click();
  await page
    .getByRole("checkbox", { name: "Fixture privacy", exact: true })
    .uncheck();
  await expect(page.getByText("Applying saved changes…")).toBeVisible();
  await page.getByRole("button", { name: "Update blocklists" }).click();
  await expect(
    page.getByText("Download failed; previous active version retained"),
  ).toBeVisible();
  await expect(
    page.getByRole("checkbox", { name: "Fixture privacy", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("cell", { name: "Not downloaded", exact: true }),
  ).toBeVisible();
});
test("timed pause sends an absolute expiry and the saved revision", async ({
  page,
}) => {
  let body:
    | { revision: string; enabled: boolean; pause_until: string }
    | undefined;
  await page.route("**/api/v1/blocking", (route) => {
    if (route.request().method() !== "PUT") return route.fallback();
    body = route.request().postDataJSON();
    return route.fulfill({ json: activation });
  });
  await page.goto("/lists");
  await page.getByRole("button", { name: "Pause filtering…" }).click();
  await page.getByLabel("Pause duration").selectOption("30");
  await page
    .getByRole("button", { name: "Pause filtering", exact: true })
    .click();
  await expect.poll(() => body?.enabled).toBe(false);
  expect(body?.revision).toBe(activation.saved_revision);
  expect(Date.parse(body!.pause_until) - Date.now()).toBeGreaterThan(
    29 * 60 * 1000,
  );
});
test("outdated edits retain their draft and revision until explicitly discarded", async ({
  page,
}) => {
  let sent: unknown;
  let current = settings;
  let reads = 0;
  await page.route("**/api/v1/settings", (route) => {
    if (route.request().method() === "PATCH") {
      sent = route.request().postDataJSON();
      return route.fulfill({
        status: 409,
        json: { code: "revision_conflict", message: "Changed on disk" },
      });
    }
    reads++;
    return route.fulfill({ json: current });
  });
  await page.goto("/settings");
  await page.getByLabel("Memory budget (bytes)").fill("4194304");
  current = {
    ...settings,
    status: { ...activation, saved_revision: "fixture-revision-2" },
  };
  const before = reads;
  await page.getByRole("button", { name: "Reload displayed data" }).click();
  await expect.poll(() => reads).toBeGreaterThan(before);
  await expect(page.getByLabel("Memory budget (bytes)")).toHaveValue("4194304");
  await page.getByRole("button", { name: "Save settings" }).click();
  await expect(page.getByRole("alert")).toContainText(
    "Settings changed since you opened this form",
  );
  expect(sent).toEqual({
    revision: settings.status.saved_revision,
    edits: [{ path: ["cache", "bytes"], value: 4194304 }],
  });
  await expect(page.getByLabel("Memory budget (bytes)")).toHaveValue("4194304");
  await expect(
    page.getByText("Settings saved.", { exact: true }),
  ).not.toBeVisible();
  await page.getByRole("button", { name: "Discard edits and reload" }).click();
  await expect(page.getByLabel("Memory budget (bytes)")).toHaveValue("8388608");
  await expect(
    page.getByRole("button", { name: "Save settings" }),
  ).toBeDisabled();
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
  await page.getByLabel("Match type").selectOption("regex");
  await page.getByLabel("Domain or pattern", { exact: true }).fill("[");
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
  await page
    .getByRole("button", { name: "Download backup", exact: true })
    .click();
  await expect(page.getByText(/Couldn’t prepare the backup/)).toBeVisible();
  await page.getByRole("button", { name: "Backup error details" }).click();
  await expect(
    page.getByText("Backup destination is not writable"),
  ).toBeVisible();
  await expect(page.getByText("No jobs have been recorded.")).toBeVisible();
  await expect(
    page.getByRole("link", { name: /Download.*backup/ }),
  ).toHaveCount(0);
});

test("settings cannot be edited before loading or without a saved revision", async ({
  page,
}) => {
  let release!: () => void;
  const wait = new Promise<void>((resolve) => {
    release = resolve;
  });
  let writes = 0;
  await page.route("**/api/v1/settings", async (route) => {
    if (route.request().method() !== "GET") writes++;
    await wait;
    return route.fulfill({
      json: { ...settings, status: { ...activation, saved_revision: "" } },
    });
  });
  await page.goto("/settings");
  await expect(
    page.getByRole("status").filter({ hasText: "Loading…" }),
  ).toBeVisible();
  await expect(page.getByLabel("Memory budget (bytes)")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Save settings" })).toHaveCount(
    0,
  );
  release();
  await expect(page.getByLabel("Memory budget (bytes)")).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "Save settings" }),
  ).toBeDisabled();
  expect(writes).toBe(0);
});

test("custom list editor waits for its revision and fits desktop and mobile", async ({
  page,
}) => {
  let release!: () => void;
  const wait = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/api/v1/lists", async (route) => {
    await wait;
    return route.fulfill({ json: { status: activation, items: [] } });
  });
  await page.goto("/lists");
  await expect(
    page.getByRole("button", { name: "Add custom URL" }),
  ).toBeDisabled();
  release();
  await page.getByRole("button", { name: /Ads & Trackers/ }).click();
  await expect(
    page.getByRole("checkbox", { name: "Unavailable fixture" }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Add custom URL" }).click();
  await expect(page.getByLabel("List URL")).toHaveValue("");
  await expect(page.getByLabel("Format", { exact: true })).toHaveValue(
    "domains",
  );
  await expect(page.getByLabel("Domain scope")).toHaveValue("exact");
  await expect(
    page.getByRole("option", { name: /Unavailable fixture/ }),
  ).toHaveCount(0);
  const dialog = page.getByRole("dialog");
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    const bounds = await dialog.boundingBox();
    expect(bounds).not.toBeNull();
    expect(bounds!.width).toBeLessThanOrEqual(width - 32);
    if (width === 1440) expect(bounds!.width).toBe(720);
    expect(
      await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth),
    ).toBe(true);
    const url = await page.getByLabel("List URL").boundingBox();
    expect(url!.width).toBeGreaterThan(bounds!.width * 0.8);
    await expect(
      dialog.getByRole("button", { name: "Add list" }),
    ).toBeInViewport();
    await page.screenshot({ path: `test-results/list-dialog-${width}.png` });
  }
  await page.getByLabel("Format", { exact: true }).selectOption("dns-adblock");
  await expect(
    dialog.getByText(/Browser filter rules are not supported/),
  ).toBeVisible();
  await page.getByLabel("Format", { exact: true }).selectOption("hosts");
  await expect(
    dialog.getByText(/Browser filter rules are not supported/),
  ).toHaveCount(0);
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).not.toBeVisible();
});

test("network list selection persists without disabling downloads used by profiles", async ({
  page,
}) => {
  let items: Record<string, unknown>[] = [];
  let revision = 1;
  const writes: { method: string; body: any }[] = [];
  let release!: () => void;
  const downloading = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/api/v1/lists", async (route) => {
    const method = route.request().method();
    if (method !== "GET") {
      const body = route.request().postDataJSON();
      writes.push({ method, body });
      expect(body.revision).toBe(`revision-${revision}`);
      if (method === "POST") {
        await downloading;
        items.push(body.item);
      } else {
        for (const edit of body.edits)
          items[Number(edit.path[0])][edit.path[1]] = edit.value;
      }
      revision++;
    }
    const status = {
      ...activation,
      saved_revision: `revision-${revision}`,
      active_revision: `revision-${revision}`,
      sources: items.map((item) => ({
        id: item.id,
        enabled: item.enabled,
        usable: item.enabled,
        rules: item.enabled ? 1234 : 0,
      })),
    };
    await route.fulfill({
      json: method === "GET" ? { items, status } : status,
    });
  });
  await page.goto("/lists");
  await page.getByRole("button", { name: /Ads & Trackers/ }).click();
  const checkbox = page.getByRole("checkbox", {
    name: "Fixture privacy",
    exact: true,
  });
  await checkbox.focus();
  await page.keyboard.press("Space");
  await expect(page.getByText("Downloading…", { exact: true })).toBeVisible();
  await expect(checkbox).toBeDisabled();
  release();
  await expect(page.getByText("Downloaded", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("cell", { name: "1,234", exact: true }),
  ).toBeVisible();
  await expect(checkbox).toBeEnabled();
  await checkbox.uncheck();
  await expect(checkbox).not.toBeChecked();
  await expect(checkbox).toBeEnabled();
  expect(items[0].enabled).toBe(true);
  expect(items[0].default_apply).toBe(false);
  await checkbox.check();
  await expect(page.getByText("Downloaded", { exact: true })).toBeVisible();
  await page.reload();
  await expect(checkbox).toBeChecked();
  await expect(checkbox).toHaveCount(1);
  expect(writes.map((write) => write.method)).toEqual([
    "POST",
    "PATCH",
    "PATCH",
  ]);
  expect(writes[0].body.item).toMatchObject({
    url: "https://example.test/list",
    dialect: "domains",
    domain_kind: "exact",
    enabled: true,
  });
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: `test-results/list-subscriptions-${width}.png`,
    });
  }
});

test("password confirmation prevents submission and a successful change signs out", async ({
  page,
}) => {
  const bodies: unknown[] = [];
  await page.route("**/api/v1/password", (route) => {
    bodies.push(route.request().postDataJSON());
    return route.fulfill({ json: {} });
  });
  await page.goto("/settings");
  await page
    .getByLabel("New password", { exact: true })
    .fill("fixture-new-password");
  await page
    .getByLabel("Confirm new password", { exact: true })
    .fill("different-password");
  await page
    .getByRole("button", { name: "Change password", exact: true })
    .click();
  await expect(page.getByRole("alert")).toContainText(
    "The passwords do not match.",
  );
  expect(bodies).toEqual([]);
  await page
    .getByLabel("Confirm new password", { exact: true })
    .fill("fixture-new-password");
  await page
    .getByRole("button", { name: "Change password", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Welcome to dimsum" }),
  ).toBeVisible();
  expect(bodies).toEqual([{ password: "fixture-new-password" }]);
  expect(
    await page.evaluate(() => sessionStorage.getItem("dimsum-csrf")),
  ).toBeNull();
});
