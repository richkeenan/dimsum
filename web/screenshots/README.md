# Browser captures

Playwright writes screenshots into ignored `web/test-results/` subdirectories.
Keep captures out of Git; CI artifacts or local output hold the images.
Git also ignores PNG files copied into this directory for local review.

`dashboard.spec.ts` captures `desktop-light.png`, `desktop-dark.png`, and
`mobile.png` using `tests/e2e/fixtures.ts`, with the clock fixed at
2026-09-21 12:00 UTC. Regenerate from the repository root:

```sh
sh scripts/build-web.sh
npm --prefix web exec -- playwright install chromium
npm --prefix web run test:e2e -- dashboard.spec.ts
```

`managed-api.spec.ts` captures `managed-overview.png` after 122 isolated loopback
DNS requests recorded in SQLite. Run its Go harness from the repository root:

```sh
DIMSUM_BROWSER_TEST=1 go test ./internal/webassets -run TestBrowserAgainstManagedRuntime -v -count=1
```

These captures show test traffic and UI layouts. They do not measure household
traffic or performance.
