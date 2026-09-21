# DNS console direction

Desktop-first instrument panel: 216px navigation, a narrow status/range toolbar,
four overview measures, one wide outcome chart, two compact ranking tables.
Keep identities left-aligned and counts right-aligned with tabular numerals.

Palette: paper #f6f5f2, surface #ffffff, ink #303b36, muted #737970,
forest #396b52, warning #996a26. Dark mode uses charcoal #202622 with the
same semantic distinction. System humanist sans for controls; monospace only
for DNS names, addresses, and revisions. 13px tables, 14px body, 26px titles.

The distinctive element is the compact stacked DNS outcome plot with explicit
missing buckets. Avoid decorative gradients and rounded-card repetition. Use
borders to group related operational information. Tables remain the primary
interaction; drawers preserve log position. All counters originate in the API.

Mockup and acceptance screenshots use isolated Playwright fixtures, never
bundled sample data. API contracts are isolated in src/lib/api.ts pending
integration with the concurrently authored OpenAPI contract.
