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
DIMSUM_BROWSER_TEST=1 go test ./internal/webassets -run TestBrowserAgainstManagedRuntime -v -count=1
go test ./internal/webassets
```

The harness starts the real admin HTTP adapter and configuration coordinator on
an ephemeral local HTTP listener with a temporary config. It does not start DNS
or use production network settings. It verifies cookie/CSRF login and logout,
scalar edits preserving comments, client names, rules/test, local records,
upstream creation, conflicts, atomic external edits, and invalid-file diagnostics.
The `real-api.spec.ts` test skips in the ordinary fixture run by design.

The managed harness uses `dimsum bootstrap` with an owner-only temporary password
file, starts `app.Service.StartManaged` on ephemeral local DNS/admin ports, and
sends 122 actual UDP DNS requests for fixture local records and blocked names.
`managed-api.spec.ts` then verifies SQLite-backed summary/chart data, cursor paging,
filters and details, observed client names, an authenticated binary backup download,
restore conflict after archive selection, and successful restore after reselecting.
No response mocking or synthetic provider is used in this managed test. Both Go
browser scenarios skip in the ordinary fixture run; output is separated under
`test-results/fixtures`, `test-results/go-api`, and `test-results/managed`.

## Contract boundary

`ClientIdentity` renders the server-selected name and device category in Devices,
Top clients, and query rows. Devices opens an evidence dialog; missing metadata
uses the generic Device icon. Discovery settings edit `naming.mdns.enabled` and
the interface string array through the shared revision-checked settings API.
The backend keeps explicit names authoritative. See
[`docs/local-policy-and-naming.md`](../docs/local-policy-and-naming.md) for CLI
equivalents and runtime behavior.

`src/lib/openapi.d.ts` is generated from `api/openapi.yaml`. `src/lib/api.ts`
normalizes nested activation status for visual components, preserves large
decimal counters, and handles CSRF/session expiry and structured errors.
Collection PATCH requests contain only changed scalar fields and use the index
and revision captured when the editor opened. This also avoids writing redacted
list URLs back while changing an enabled flag. Conflict reload closes collection
editors so an old index cannot target a different item. Settings drafts survive
reload and require another explicit save.

Statistics types and fixture shapes now derive from the concrete OpenAPI schemas
matching `internal/app/provider.go`: `queries`/`fresh` summary counters, nested
`points[].outcomes`, decimal `duration_us`, string IDs/generations, and separate
configured/observed client collections. Null gap counters remain gaps. Query
filters include only the supported nonempty name/client/outcome/qtype fields;
empty cursors are omitted. Ranges remain exact across every panel, including
partial first/last buckets, and the chosen chart resolution keeps requests below
1500 points. Microseconds are formatted using integer/string arithmetic; IDs and
counter values are never converted through unsafe JavaScript numbers. Numeric
conversion is confined to bounded percentage/plot geometry. Configured names and
DNS-observed addresses are not presented as a physical-device inventory.

Jobs send the documented `backup`, `restore`, or `refresh` kind. Completed backup
jobs expose an authenticated same-origin download link; superseded artifacts are
marked expired. Restore selects a real `.tar` file (maximum 2 MiB), reads the saved
revision at file selection, and sends `{revision, archive}` with base64 archive
bytes. The captured revision is not silently refreshed at submit. Job status
polling is bounded to active jobs. An absent hook produces a visible API error.
Upstream probes and support-bundle exports are not implemented
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
load test, Pi build, household discovery coverage, or release qualification is
claimed. Those longer acceptance checks remain integration work.

Verified on 2026-09-21: typecheck, 11 unit/hook tests, production build, 11 fixture
browser scenarios, and both opt-in real Go API browser scenarios passed. Embedded
SPA/cache/HEAD/ETag/namespace tests also passed in a compiled Go test binary with
`PATH=/nonexistent`, proving that asset serving does not invoke Node. Diagnostics,
chart, and job chunks are loaded separately. These are local build results, not LAN
performance measurements.
