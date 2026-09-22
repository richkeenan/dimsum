import { readFile, writeFile } from "node:fs/promises";
import { test, expect } from "../../web/e2e";
test.skip(
  !process.env.DIMSUM_E2E_MANAGED_CONFIG,
  "Requires the isolated managed Go runtime harness",
);
test("managed DNS history, cursor filters, observed names, backup download and archive restore", async ({
  page,
}, testInfo) => {
  test.setTimeout(60000);
  const file = process.env.DIMSUM_E2E_MANAGED_CONFIG!;
  const failures: string[] = [];
  page.on("response", (r) => {
    if (r.url().includes("/api/v1/") && r.status() >= 400 && r.status() !== 401)
      failures.push(`${r.status()} ${r.url()}`);
  });
  await page.goto("/");
  await page
    .getByLabel("Admin password")
    .fill(process.env.DIMSUM_E2E_PASSWORD!);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Welcome to dimsum", exact: true }),
  ).not.toBeVisible();
  await expect(async () => {
    await page.getByRole("button", { name: "Reload displayed data" }).click();
    await expect(
      page
        .getByText("Total queries", { exact: true })
        .locator("..")
        .locator("strong"),
    ).toHaveText("122");
  }).toPass({ timeout: 10000 });
  await expect(
    page.getByRole("button", { name: "ads.example.test", exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("img", { name: /DNS outcomes/ })).toBeVisible();
  await page.getByText("View traffic as a table").click();
  await expect(
    page.getByRole("columnheader", { name: "Local answer", exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: testInfo.outputPath("managed-overview.png"),
    fullPage: true,
  });
  await page.getByRole("link", { name: "Query log", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Next", exact: true }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await expect(page.getByText("Page 2 · up to 100 queries")).toBeVisible();
  await page.getByLabel("Filter name").fill("ads.example.test");
  await expect(
    page.getByRole("button", { name: "ads.example.test", exact: true }),
  ).toHaveCount(12);
  await expect(
    page.getByRole("button", { name: "Next", exact: true }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "ads.example.test", exact: true })
    .first()
    .click();
  await page.getByRole("dialog").getByText("Matched rule details", { exact: true }).click();
  await expect(page.getByRole("dialog").locator("pre")).toContainText("custom:browser-block");
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await page.getByRole("link", { name: "Devices", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Devices", exact: true, level: 2 }),
  ).toBeVisible();
  await expect(
    page.getByRole("cell", { name: "122", exact: true }),
  ).toBeVisible();
  expect(failures).toEqual([]);
  await page.getByRole("link", { name: "Backups", exact: true }).click();
  await page
    .getByRole("button", { name: "Create backup", exact: true })
    .click();
  const downloadLink = page.getByRole("link", {
    name: "Download backup",
    exact: true,
  });
  await expect(downloadLink).toBeVisible();
  const downloadPromise = page.waitForEvent("download");
  await downloadLink.click();
  const download = await downloadPromise;
  const archive = testInfo.outputPath("configuration.tar");
  await download.saveAs(archive);
  const bytes = await readFile(archive);
  expect(bytes.length).toBeGreaterThan(512);
  expect(bytes.length).toBeLessThanOrEqual(2 * 1024 * 1024);
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  await page.getByLabel("Maximum expired-answer age (seconds)").fill("180");
  await page
    .getByRole("button", { name: "Save settings", exact: true })
    .click();
  await expect(
    page.getByRole("status").filter({ hasText: "Settings saved." }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Backups", exact: true }).click();
  await page
    .getByLabel("Configuration archive", { exact: false })
    .setInputFiles(archive);
  await expect(
    page.getByRole("button", { name: "Validate and restore" }),
  ).toBeEnabled();
  // Change disk AFTER file selection: restore must retain the captured revision.
  const edited = await readFile(file, "utf8");
  await writeFile(
    file,
    edited.replace("max_stale_seconds: 180", "max_stale_seconds: 240"),
  );
  await page.getByRole("button", { name: "Validate and restore" }).click();
  await expect(page.getByRole("alert")).toContainText(/revision|conflict/i);
  expect(await readFile(file, "utf8")).toContain("max_stale_seconds: 240");
  await page
    .getByLabel("Configuration archive", { exact: false })
    .setInputFiles([]);
  await page
    .getByLabel("Configuration archive", { exact: false })
    .setInputFiles(archive);
  await expect(
    page.getByRole("button", { name: "Validate and restore" }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "Validate and restore" }).click();
  await expect
    .poll(async () => readFile(file, "utf8"))
    .toContain("max_stale_seconds: 3600");
  await expect(
    page.getByRole("status").filter({ hasText: "Operation completed." }),
  ).toBeVisible();
});
