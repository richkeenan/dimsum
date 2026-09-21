# dimsum administration UI

React/TypeScript, Vite, Tailwind CSS v4, and locally copied shadcn/ui components
using the Radix primitive family. All direct dependencies are exact versions;
`package-lock.json` pins the transitive graph. Node is build/test-time only.

## Development and verification

```sh
cd web
npm ci
npm run api:generate
npm run typecheck
npm run test
npm run build
npx playwright install chromium
npm run test:e2e
```

`npm run dev` serves the UI and proxies `/api` and `/session` to
`http://127.0.0.1:8080`. The authenticated server must allow the browser's exact
Host/Origin when using that proxy. `npm run preview` serves built assets only;
Playwright fixtures intercept requests and never enter the production bundle.

The production output is **`internal/webassets/dist/`**. Commit this directory
after a source change so Go builds work without Node. Builds use two Playwright
workers; for constrained build hosts set `NODE_OPTIONS=--max-old-space-size=1536`
and `GOMAXPROCS=2`. A Pi build has not been measured.

From the repository root, run the opt-in real API browser harness:

```sh
DIMSUM_BROWSER_TEST=1 go test ./internal/webassets -run TestBrowserAgainstGoAPI -v -count=1
go test ./internal/webassets
```

The harness starts the real admin HTTP adapter and configuration coordinator on
an ephemeral local HTTP listener with a temporary config. It does not start DNS
or use production network settings. It verifies cookie/CSRF login and logout,
scalar edits preserving comments, client names, rules/test, local records,
upstream creation, conflicts, atomic external edits, and invalid-file diagnostics.
The `real-api.spec.ts` test skips in the ordinary fixture run by design.

## Contract boundary

`src/lib/openapi.d.ts` is generated from `api/openapi.yaml`. `src/lib/api.ts`
normalizes nested activation status for visual components, preserves large
decimal counters, and handles CSRF/session expiry and structured errors.
Collection PATCH requests contain only changed scalar fields and use the index
and revision captured when the editor opened. This also avoids writing redacted
list URLs back while changing an enabled flag. Conflict reload closes collection
editors so an old index cannot target a different item. Settings drafts survive
reload and require another explicit save.

The current OpenAPI statistics provider responses are open objects. The proposed
view types (`Summary`, `Page`, timeseries `buckets`, ranking `clients`/`domains`)
and illustrative provider fixtures are explicit in `src/lib/api.ts` and
`tests/e2e/fixtures.ts`. They require alignment with a concrete history provider;
missing values display as unavailable, never invented traffic. Configured client
names are not presented as a discovered physical-device inventory.

Jobs send the documented `backup`, `restore`, or `refresh` kind with a JSON
`input`; that input remains adapter-specific until backup hooks publish a concrete
schema. Job status polling is bounded to active jobs. An absent hook produces a
visible API error. Upstream probes and support-bundle exports are not implemented
by the current API, so the UI does not fabricate results for them. Settings can
edit existing scalar paths for cache, listener, naming, and retention values;
the coordinator reports unsupported/missing-path edits precisely.

## UI evidence and outstanding acceptance

Fixture browser checks cover desktop light/dark, 390px layout without horizontal
page overflow, consistent time ranges, cursor filters/detail, explicit exact vs
suffix preview, authentication expiry, incomplete history, unavailable storage,
conflicts, rejected regex, manual names, pending list edits/failed refresh, timed
pause, and failed backup. Hook tests cover request cancellation and bounded SSE
updates, reconnect status, and stream cleanup. Screenshots in `screenshots/` are
labelled fixture renderings, not a real household traffic report.

No one-hour browser heap soak, household 200-row p95 measurement, DNS coexistence
load test, Pi build, discovery coverage, successful restore-hook test, or release
qualification is claimed. API provider wiring and those longer acceptance checks
remain integration work.

Verified on 2026-09-21: typecheck, 7 unit/hook tests, production build, 11 fixture
browser scenarios, and the opt-in real Go API browser scenario passed. Embedded
SPA/cache/HEAD/ETag/namespace tests also passed in a compiled Go test binary with
`PATH=/nonexistent`, proving that asset serving does not invoke Node. The base
production JS bundle was 328.54 kB (106.00 kB gzip); diagnostics, chart, and job
chunks are loaded separately. These are local build results, not LAN performance
measurements.
