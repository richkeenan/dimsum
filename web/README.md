# Dashboard development

The dimsum dashboard uses React, TypeScript, TanStack Router/Start, Vite, and
Tailwind CSS. It builds as a static SPA embedded in the Go executable. Node is
required for development and builds, not for running an installed server.

## Setup

Use Node 24.21.0 and npm. From `web/`:

```sh
npm ci
npm run dev
```

Vite serves the dashboard on `http://127.0.0.1:5173` and proxies `/api` and
`/session` to `http://127.0.0.1:8080`. Start an isolated dimsum backend with the
[development configuration](../guides/configuration.md#minimal-local-development-configuration)
and sign in with `admin` on first startup. That configuration admits the Vite Host/Origin.
To use a different backend:

```sh
DIMSUM_API_URL=http://127.0.0.1:18080 npm run dev
```

Use local test configuration and data directories. The dev proxy sends real
administrative requests to its backend.

## Checks and builds

```sh
npm run api:generate
npm run typecheck
npm test
npm run build
npx playwright install chromium
npm run test:e2e
```

The build copies `web/dist/client/` into `internal/webassets/dist/` for embedding.
Both directories are ignored. From the repository root,
`sh scripts/build-web.sh` installs dependencies and builds the UI; run it before
Go tests on a fresh checkout or after frontend changes. GoReleaser and Docker
builds generate assets as part of their build process.

`npm run preview` serves built frontend output. It is useful for the fixture
browser tests; use `npm run dev` with its proxy for interactive backend development.

## Test modes

- `npm test`: component, formatting, API-client, and hook tests.
- `npm run test:e2e`: Playwright scenarios with intercepted API responses.
- Go API browser tests: real sessions, CSRF, configuration edits, conflicts,
  streaming, and managed DNS/history/backup flows on temporary local listeners.

Run both real API browser scenarios from the repository root:

```sh
DIMSUM_BROWSER_TEST=1 go test ./internal/webassets -run 'TestBrowserAgainst(GoAPI|ManagedRuntime)$' -v -count=1
```

They skip in ordinary Go/fixture runs. Screenshots and reports go to ignored
`web/test-results/` directories; see [screenshots/README.md](screenshots/README.md).
The scenarios use synthetic traffic and do not require a household server.

## API and generated files

`api/openapi.yaml` is the HTTP contract. Commit `src/lib/openapi.d.ts` after
`npm run api:generate`. Vite updates `src/routeTree.gen.ts` during development and
builds; commit route changes with their generated tree. Keeping these files in
Git lets editors and standalone typechecks work before a build.

`src/lib/api.ts` handles sessions, CSRF, structured errors, and activation status.
Preserve decimal strings for large counters and IDs. Configuration editors must
use the revision captured when the draft opened and handle conflicts explicitly.
See [the contributor guide](../CONTRIBUTING.md) for shared backend conventions.

## Visual design and licenses

Follow [DESIGN.md](DESIGN.md) for layout and typography. Copied components live in
`src/components/ui/`; retain the notices in [THIRD_PARTY.md](THIRD_PARTY.md).
