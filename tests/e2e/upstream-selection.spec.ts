import { test, expect } from "../../web/e2e";
import { fixtureAPI, settings, activation } from "./fixtures";

test("priority changes stay local, preserve rows and do not flash the page controls", async ({
  page,
}) => {
  await fixtureAPI(page);
  let addresses = ["192.0.2.53:53", "192.0.2.54:53"];
  let saved = false;
  let releaseWrite!: () => void;
  let releaseRead!: () => void;
  const writeGate = new Promise<void>((resolve) => {
    releaseWrite = resolve;
  });
  const readGate = new Promise<void>((resolve) => {
    releaseRead = resolve;
  });
  const requests: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/api/v1/"))
      requests.push(new URL(request.url()).pathname);
  });
  const status = () => ({
    ...activation,
    saved_revision: saved ? "r2" : "r1",
    active_revision: saved ? "r2" : "r1",
  });
  await page.route("**/api/v1/settings", async (route) => {
    if (saved) await readGate;
    await route.fulfill({
      json: {
        ...settings,
        status: status(),
        config: { dns: { upstreams: addresses } },
      },
    });
  });
  await page.route("**/api/v1/upstreams", async (route) => {
    if (route.request().method() === "PATCH") {
      const body = route.request().postDataJSON();
      expect(body.revision).toBe("r1");
      await writeGate;
      addresses = ["192.0.2.54:53", "192.0.2.53:53"];
      saved = true;
      await route.fulfill({ json: status() });
    } else {
      if (saved) await readGate;
      await route.fulfill({ json: { status: status(), items: addresses } });
    }
  });
  await page.goto("/upstreams");
  const add = page.getByRole("button", { name: "Add upstream", exact: true });
  const down = page.getByRole("button", {
    name: "Move 192.0.2.53:53 down",
    exact: true,
  });
  await expect(down).toBeEnabled();
  const row = await page
    .getByRole("row")
    .filter({ hasText: "192.0.2.53:53" })
    .elementHandle();
  const table = await page.getByRole("table").elementHandle();
  const before = await page.getByRole("table").boundingBox();
  const opacity = await add.evaluate((el) => getComputedStyle(el).opacity);
  requests.length = 0;
  await down.click();
  await expect(page.getByRole("row").nth(1)).toContainText("192.0.2.54:53");
  expect(await add.evaluate((el) => getComputedStyle(el).opacity)).toBe(
    opacity,
  );
  expect(await table!.evaluate((el) => el.isConnected)).toBe(true);
  expect(await row!.evaluate((el) => el.isConnected)).toBe(true);
  expect((await page.getByRole("table").boundingBox())!.y).toBe(before!.y);
  await expect(
    page.getByRole("status").filter({ hasText: /Saving|saved/ }),
  ).toHaveCount(0);
  releaseWrite();
  await expect.poll(() => saved).toBe(true);
  await expect(page.getByText("Changes saved.", { exact: true })).toHaveCount(
    0,
  );
  await expect(
    page.getByRole("status").filter({ hasText: /Saving|saved/ }),
  ).toHaveCount(0);
  releaseRead();
  await expect(
    page.getByRole("button", { name: "Move 192.0.2.53:53 up", exact: true }),
  ).toBeEnabled();
  expect(await row!.evaluate((el) => el.isConnected)).toBe(true);
  expect(await table!.evaluate((el) => el.isConnected)).toBe(true);
  await expect(down).toBeFocused();
  await expect(down).toHaveAttribute("aria-disabled", "true");
  const count = requests.length;
  await down.press("Enter");
  expect(requests.length).toBe(count);
  expect((await page.getByRole("table").boundingBox())!.y).toBe(before!.y);
  expect(
    requests.filter(
      (path) => !["/api/v1/settings", "/api/v1/upstreams"].includes(path),
    ),
  ).toEqual([]);
});

test("selection help uses available desktop width and still fits mobile", async ({
  page,
}) => {
  await fixtureAPI(page);
  await page.setViewportSize({ width: 1800, height: 1000 });
  await page.goto("/upstreams");
  const help = page.locator("#upstream-selection-help");
  await expect(help).toBeVisible();
  expect((await help.boundingBox())!.height).toBeLessThan(30);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(help).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
});
