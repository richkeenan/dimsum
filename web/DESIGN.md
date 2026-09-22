# DNS console direction

Desktop-first instrument panel: 240px navigation, a narrow status/range toolbar,
five overview measures, one wide outcome chart, two compact ranking tables.
Keep identities left-aligned and counts right-aligned with tabular numerals.

Palette: paper #f6f5f2, surface #ffffff, ink #303b36, muted #737970,
forest #396b52, warning #996a26. Dark mode uses charcoal #202622 with the
same semantic distinction. System humanist sans for controls; monospace only
for DNS names, addresses, and revisions. At the default browser font size, use
16px body, navigation, controls, and table data; 14px minimum for secondary text;
26px page titles. Keep the root font size at 100% to respect browser preferences.
The shared `text-xs` and `text-sm` tokens mean secondary (0.875rem) and regular
(1rem) text, both with 1.5 line height. Avoid smaller per-page font overrides.

The distinctive element is the compact stacked DNS outcome plot with explicit
missing buckets. Avoid decorative gradients and rounded-card repetition. Use
borders to group related operational information. Tables remain the primary
interaction; drawers preserve log position. All counters originate in the API.

Use isolated Playwright fixtures for screenshots. Keep fixture data out of the
production bundle. The API client in `src/lib/api.ts` uses generated types from
`api/openapi.yaml`; preserve those types at feature boundaries.
