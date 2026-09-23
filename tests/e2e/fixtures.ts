import type { Page } from "../../web/e2e";
import type {
  Activation,
  Summary,
  QueryDetail,
  Series,
  Rankings,
  ClientsResponse,
  Performance,
} from "../../web/src/lib/api";
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
  range: { from: "2026-09-20T12:00:00Z", to: "2026-09-21T12:00:00Z" },
  queries: "48216",
  blocked: "12144",
  fresh: "27864",
  stale: "18",
  rejected: "7",
  duration_us: "12345678",
  complete: true,
  updated_at: "2026-09-21T12:00:00Z",
} satisfies Summary;
export const settings = {
  status: activation,
  config: { cache: { bytes: 8388608 }, dns: { listen: ["127.0.0.1:5353"] } },
};
export const policyFixture = {
  status: activation,
  scope: "client",
  id: "study-laptop",
  desired: {
    id: "study-laptop",
    name: "Study laptop",
    selectors: { addresses: ["192.0.2.12"] },
  },
  effective: {
    id: "study-laptop",
    profile_id: "",
    blocking: { value: true, source: {} },
    filtering: true,
    paused_until: "0001-01-01T00:00:00Z",
    global_paused: false,
    lists: {},
    upstream_source: {},
    route_id: "route",
    upstream: { upstreams: ["192.0.2.53:53"] },
    override_count: 0,
    rules: [],
  },
  active: null,
};
export const performance = {
  range: summary.range,
  updated_at: summary.updated_at,
  complete: false,
  resolution_seconds: 3600,
  summary: {
    count: "23000",
    average_us: "8700",
    p50_us: "80",
    p95_us: "36000",
    p99_us: "125000",
    percentiles_available: true,
  },
  outcomes: [
    {
      outcome: "local",
      count: "500",
      average_us: "22",
      p50_us: "20",
      p95_us: "30",
      p99_us: "45",
      percentiles_available: true,
    },
    {
      outcome: "blocked",
      count: "3500",
      average_us: "34",
      p50_us: "28",
      p95_us: "60",
      p99_us: "100",
      percentiles_available: true,
    },
    {
      outcome: "cache",
      count: "13500",
      average_us: "75",
      p50_us: "60",
      p95_us: "180",
      p99_us: "400",
      percentiles_available: true,
    },
    {
      outcome: "stale",
      count: "50",
      average_us: "150",
      p50_us: "130",
      p95_us: "300",
      p99_us: "500",
      percentiles_available: true,
    },
    {
      outcome: "forwarded",
      count: "5400",
      average_us: "36000",
      p50_us: "24000",
      p95_us: "110000",
      p99_us: "240000",
      percentiles_available: true,
    },
    {
      outcome: "error",
      count: "50",
      average_us: "1100000",
      p50_us: "1000000",
      p95_us: "2000000",
      p99_us: "2000000",
      percentiles_available: true,
    },
  ],
  distribution: [
    "12000",
    "3000",
    "1000",
    "2000",
    "1000",
    "3500",
    "450",
    "50",
  ].map((count, i) => ({
    count,
    lower_us: ["0", "100", "500", "1000", "5000", "10000", "100000", "1000000"][
      i
    ],
    upper_us: [
      "100",
      "500",
      "1000",
      "5000",
      "10000",
      "100000",
      "1000000",
      null,
    ][i],
  })),
  points: Array.from({ length: 24 }, (_, i) => ({
    time: new Date(Date.UTC(2026, 8, 20, 12 + i)).toISOString(),
    count: i === 17 ? "0" : "1000",
    average_us: i === 17 ? null : String(4000 + ((i * 719) % 6000)),
    p50_us: i === 17 ? null : String(50 + ((i * 17) % 150)),
    p95_us: i === 17 ? null : String(20000 + ((i * 17919) % 45000)),
    p99_us: i === 17 ? null : String(75000 + ((i * 35179) % 150000)),
    percentiles_available: i !== 17,
    complete: i !== 17 && i !== 23,
    gap: i === 17,
  })),
} satisfies Performance;
export const query = {
  id: "9007199254740993",
  boot_id: "fixture-boot",
  sequence: "9007199254740993",
  time: "2026-09-21T11:58:01Z",
  name: "telemetry.example.test",
  client: "192.0.2.12",
  client_name: "Study laptop",
  client_name_source: "manual",
  client_name_fresh: true,
  qtype: "A",
  qtype_code: 1,
  qclass: 1,
  rcode: 3,
  flags: 0,
  outcome: "blocked",
  duration_us: "180",
  generation: "42",
  rule_id: "9",
  upstream_id: "0",
  rule_description:
    "id: custom:privacy-rule\nkind: exact\npattern: telemetry.example.test",
  rule_description_available: true,
  alias_available: false,
  alias_chain_available: false,
  upstream_attempts_available: false,
  cache_age_available: false,
  ad_trust_available: false,
  updated_at: summary.updated_at,
} satisfies QueryDetail;
export async function fixtureAPI(page: Page) {
  await page.addInitScript(() =>
    sessionStorage.setItem("dimsum-csrf", "fixture-csrf"),
  );
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(
      "/api/v1/",
      "",
    );
    const values: Record<string, unknown> = {
      tokens: { items: [] },
      profiles: { status: activation, items: [] },
      "client-policy": policyFixture,
      summary,
      performance,
      settings,
      timeseries: {
        complete: false,
        range: summary.range,
        updated_at: summary.updated_at,
        resolution_seconds: 3600,
        points: Array.from({ length: 24 }, (_, i) => ({
          time: new Date(Date.UTC(2026, 8, 20, 12 + i)).toISOString(),
          outcomes:
            i === 17
              ? null
              : {
                  local: "21",
                  forwarded: String(150 + ((i * 71) % 300)),
                  blocked: String(75 + ((i * 31) % 120)),
                  cache: String(220 + ((i * 29) % 300)),
                  stale: "0",
                  error: "0",
                  rejected: "0",
                },
          duration_us: i === 17 ? null : "123456",
          histogram: i === 17 ? null : ["1", "2", "3", "4", "5", "6", "7", "8"],
          gap: i === 17,
          complete: i !== 17,
        })),
      } satisfies Series,
      rankings: {
        complete: true,
        range: summary.range,
        updated_at: summary.updated_at,
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
      } satisfies Rankings,
      queries: { items: [query], next_cursor: "second-page" },
      ["queries/" + query.id]: query,
      clients: {
        status: activation,
        observed_available: true,
        observed: {
          complete: true,
          truncated: false,
          range: summary.range,
          updated_at: summary.updated_at,
          items: [
            {
              address: "192.0.2.12",
              client_id: "study-laptop",
              matching_method: "address",
              name: "Study laptop",
              name_source: "manual",
              name_fresh: true,
              count: "9100",
              blocked: "1420",
              last_seen: query.time,
            },
          ],
        },
        items: [
          {
            name: "Study laptop",
            policy_id: "study-laptop",
            id: "study-laptop",
            address: "192.0.2.12",
          },
        ],
      } satisfies ClientsResponse,
      lists: { status: activation, items: [] },
      catalog: {
        items: [
          {
            id: "privacy",
            label: "Fixture privacy",
            url: "https://example.test/list",
            dialect: "domains",
            domain_kind: "exact",
            available: true,
            default_enabled: true,
            description: "Fixture domain blocklist.",
          },
          {
            id: "unavailable",
            label: "Unavailable fixture",
            available: false,
            unavailable_reason: "Not available on this installation",
          },
        ],
      },
      password: {},
      rules: { status: activation, items: [] },
      records: { status: activation, items: [] },
      upstreams: { status: activation, items: [] },
      jobs: { items: [] },
      diagnostics: {
        dns_ready: true,
        dns_addresses: ["192.0.2.53:53"],
        storage: { available: true, writer: { LastError: "" } },
        version: "fixture",
      },
      blocking: { enabled: true, status: activation },
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
