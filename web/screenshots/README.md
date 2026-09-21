# Fixture renderings

`desktop-light.png`, `desktop-dark.png`, and `mobile.png` show the implemented UI
using `tests/e2e/fixtures.ts`, with the clock fixed at 2026-09-21 12:00 UTC.
These are design/desktop acceptance artifacts, not measured household traffic.
Regenerate with the dashboard Playwright test after a production build.

`managed-overview.png` is a browser capture of the actual managed daemon, after
122 isolated loopback DNS requests recorded in SQLite. It includes the accessible
traffic table and honestly incomplete coverage from starting a fresh service.
Regenerate with `TestBrowserAgainstManagedRuntime`; it is test traffic, not
household traffic or a performance measurement.
