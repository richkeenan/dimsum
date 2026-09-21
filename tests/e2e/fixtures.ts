import type { Page } from "../../web/e2e";
import type { Activation } from "../../web/src/lib/api";
export const activation: Activation = {
  saved_revision: "fixture-revision-1",
  active_revision: "fixture-revision-1",
  active_generation: "42",
  pending: false,
  recovered: false,
  restart_required: false,
  sources: [],
};
export const summary = {
  total: "48216",
  blocked: "12144",
  cached: "27864",
  stale: "18",
  errors: "7",
  active_clients: "24",
  dns: "Healthy",
  list_health: "Current",
  storage_health: "Persisting",
  blocking: true,
  complete: true,
  updated_at: "2026-09-21T12:00:00Z",
};
export const settings = {
  status: activation,
  config: { cache: { bytes: 8388608 }, dns: { listen: ["127.0.0.1:5353"] } },
};
export const query = {
  id: "9007199254740993",
  time: "2026-09-21T11:58:01Z",
  name: "telemetry.example.test",
  client: "192.0.2.12",
  client_name: "Study laptop",
  qtype: "A",
  outcome: "blocked",
  reason: "custom deny",
  duration_ms: "0.18",
  upstream: "—",
  freshness: "fresh",
  policy_generation: "42",
  winning_rule: "privacy-rule",
  response_mode: "NXDOMAIN",
  ad_trusted: false,
};
export async function fixtureAPI(page: Page) {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(
      "/api/v1/",
      "",
    );
    const values: Record<string, unknown> = {
      summary,
      settings,
      timeseries: {
        complete: true,
        buckets: Array.from({ length: 48 }, (_, i) => ({
          time: new Date(Date.UTC(2026, 8, 21, 0, i * 30)).toISOString(),
          forwarded: String(150 + ((i * 71) % 300)),
          blocked: String(75 + ((i * 31) % 120)),
          cached: String(220 + ((i * 29) % 300)),
          stale: "0",
          errors: "0",
          missing: i === 17,
        })),
      },
      rankings: {
        clients: [
          "Study laptop",
          "Living room TV",
          "Home server",
          "Kitchen speaker",
          "Office desktop",
          "Tablet",
          "Phone",
          "Guest laptop",
          "Printer",
          "Media player",
        ].map((name, i) => ({
          name,
          address: `192.0.2.${12 + i}`,
          count: String(9100 - i * 750),
        })),
        domains: Array.from({ length: 10 }, (_, i) => ({
          name:
            [
              "telemetry",
              "ads",
              "metrics",
              "tracking",
              "events",
              "analytics",
              "beacon",
              "log",
              "pixel",
              "collect",
            ][i] + ".example.test",
          count: String(1900 - i * 170),
        })),
      },
      queries: { items: [query], next_cursor: "second-page" },
      ["queries/" + query.id]: query,
      clients: {
        status: activation,
        items: [
          {
            name: "Study laptop",
            address: "192.0.2.12",
            source: "manual",
            freshness: "current",
            queries: "9100",
            blocked: "1420",
          },
        ],
      },
      lists: { status: activation, items: [] },
      rules: { status: activation, items: [] },
      records: { status: activation, items: [] },
      upstreams: { status: activation, items: [] },
      jobs: { items: [] },
      diagnostics: {
        dns: "healthy",
        storage: "persisting",
        version: "fixture",
      },
    };
    if (path === "events") {
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: "event: summary\ndata: {}\n\n",
      });
      return;
    }
    await route.fulfill({
      json: values[path] ?? {},
      status: path in values ? 200 : 404,
    });
  });
  await page.route("**/session", (route) =>
    route.fulfill({
      json: { csrf_token: "fixture-csrf", expires_at: "2026-09-22T12:00:00Z" },
    }),
  );
}
