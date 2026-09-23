import { test, expect } from "../../web/e2e";
import { activation, fixtureAPI } from "./fixtures";

test("compiled device rows sum large counters exactly and show zero without observations", async ({
  page,
}) => {
  await fixtureAPI(page);
  await page.route("**/api/v1/clients?**", (route) =>
    route.fulfill({
      json: {
        status: activation,
        items: [
          { id: "laptop", policy_id: "laptop", name: "Counter laptop" },
          { id: "idle", policy_id: "idle", name: "Idle device" },
        ],
        observed_available: true,
        observed: {
          complete: true,
          truncated: false,
          items: [
            { address: "192.0.2.10", client_id: "laptop", count: "9007199254740993", blocked: "5" },
            { address: "192.0.2.11", client_id: "laptop", count: "2", blocked: "7" },
          ],
        },
      },
    }),
  );
  await page.goto("/clients");
  const laptop = page.getByRole("row").filter({ hasText: "Counter laptop" });
  await expect(
    laptop.getByRole("cell", { name: "9,007,199,254,740,995", exact: true }),
  ).toBeVisible();
  await expect(laptop.getByRole("cell", { name: "12", exact: true })).toBeVisible();
  const idle = page.getByRole("row").filter({ hasText: "Idle device" });
  await expect(idle.getByRole("cell", { name: "0", exact: true })).toHaveCount(2);
});
