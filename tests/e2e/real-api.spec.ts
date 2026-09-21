import { readFile, writeFile, rename } from "node:fs/promises";
import { test, expect } from "../../web/e2e";
test.skip(
  !process.env.DIMSUM_E2E_CONFIG,
  "Run through the isolated Go webassets browser harness",
);
test("real Go authentication, scalar text edit, collection writes, conflicts and file activation", async ({
  page,
}) => {
  const file = process.env.DIMSUM_E2E_CONFIG!;
  const original = await readFile(file, "utf8");
  await page.goto("/settings");
  await page
    .getByLabel("Admin password")
    .fill(process.env.DIMSUM_E2E_PASSWORD!);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Welcome to dimsum", exact: true }),
  ).not.toBeVisible();
  await expect(
    page.getByLabel("Maximum expired-answer age (seconds)"),
  ).toBeEnabled();
  await page.getByLabel("Maximum expired-answer age (seconds)").fill("120");
  await page.getByRole("button", { name: "Save settings" }).click();
  await expect(
    page.getByRole("status").filter({ hasText: "Settings saved." }),
  ).toBeVisible();
  const edited = await readFile(file, "utf8");
  expect(edited).toContain("# Isolated local skeleton");
  expect(edited).toContain("max_stale_seconds: 120");
  expect(edited.split("\n").filter((l) => l.trim().startsWith("#"))).toEqual(
    original.split("\n").filter((l) => l.trim().startsWith("#")),
  );
  await page.getByRole("link", { name: "Devices", exact: true }).click();
  await page.getByRole("button", { name: "Name an address" }).click();
  await page.getByLabel("Observed address").fill("192.0.2.12");
  await page
    .getByLabel("Friendly name", { exact: true })
    .fill("Browser workstation");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await expect(
    page.getByRole("cell", {
      name: "Browser workstation 192.0.2.12",
      exact: true,
    }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Custom rules", exact: true }).click();
  await page.getByRole("button", { name: "Add rule" }).click();
  await page
    .getByLabel("Domain or pattern", { exact: true })
    .fill("ads.example.test");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  const rules = await page.evaluate(async () =>
    (await fetch("/api/v1/rules")).json(),
  );
  const rule = rules.items.find(
    (item: { pattern: string }) => item.pattern === "ads.example.test",
  );
  expect(rule?.id).toBeTruthy();
  await page.getByLabel("Domain", { exact: true }).fill("ads.example.test");
  await page.getByRole("button", { name: "Test rule", exact: true }).click();
  await expect(
    page.getByRole("status").getByText("Blocked", { exact: true }),
  ).toBeVisible();
  await page.getByText("Match details", { exact: true }).click();
  await expect(
    page.getByText(`custom:${rule.id}`, { exact: false }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Local DNS", exact: true }).click();
  await page.getByRole("button", { name: "Add record" }).click();
  await page.getByLabel("DNS name").fill("printer.home.arpa");
  await page.getByLabel("Address or target").fill("192.0.2.20");
  await page.getByLabel("Cache lifetime (seconds)").fill("60");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await expect(
    page.getByRole("cell", { name: "printer.home.arpa", exact: true }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Upstreams", exact: true }).click();
  await page.getByRole("button", { name: "Add upstream" }).click();
  await page.getByLabel("Address and port").fill("192.0.2.53:53");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await expect(
    page.getByRole("cell", { name: "192.0.2.53:53", exact: true }),
  ).toBeVisible();
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
  await page
    .getByRole("button", { name: "Discard edits and reload", exact: true })
    .click();
  await expect(
    page.getByLabel("Maximum expired-answer age (seconds)"),
  ).toHaveValue("240");
  await expect
    .poll(async () =>
      page.evaluate(async () => {
        const s = await (await fetch("/api/v1/settings")).json();
        return s.status.saved_revision === s.status.active_revision;
      }),
    )
    .toBe(true);
  await writeFile(
    file,
    (await readFile(file, "utf8")) + "unknown_setting: true\n",
  );
  await page.getByRole("button", { name: "Refresh all data" }).click();
  await expect(page.getByRole("alert")).toContainText("unknown_setting");
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Welcome to dimsum", exact: true }),
  ).toBeVisible();
});
