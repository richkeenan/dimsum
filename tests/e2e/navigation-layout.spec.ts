import { test, expect } from "../../web/e2e";
import { fixtureAPI } from "./fixtures";

test.beforeEach(async ({ page }) => fixtureAPI(page));

test("keeps DNS server details in the desktop sidebar instead of the header", async ({
  page,
}) => {
  await page.goto("/");

  const sidebar = page.locator("aside");
  await expect(sidebar.getByRole("button", { name: "DNS server" })).toBeVisible();
  await expect(
    page.locator("header").getByRole("button", { name: "DNS server" }),
  ).toHaveCount(0);
});
