# Historical dashboard queries

Overview preserves the exact selected time range, including seconds and
milliseconds. It reads precomputed history where possible:

- Summary totals combine complete day, hour, and minute buckets with individual
  query records for the sub-minute edges.
- Rankings combine complete hourly counts with individual query records for the
  partial-hour edges. Counts for every identity are merged before selecting the
  top ten, with stable identity ordering for ties.
- `GET /api/v1/rankings` also returns `active_clients`, a decimal string counting
  distinct addresses with retained admitted queries. It is not capped at ten or
  200. Overview uses this field instead of fetching the full client inventory.

Each summary or rankings request uses one database snapshot. Missing observation
coverage or expired data sets `complete` to `false`; available counts are still
returned. Expired aggregate interiors fall back to finer retained history when
retention durations differ by tier. A retained aggregate can survive after its
underlying detail expires,
but it cannot supply exact counts for a partial bucket. Active clients therefore
describes the available history, not a list of devices currently online.

The seven-day preset refreshes every 30 seconds. The one-hour and 24-hour presets
refresh every five seconds. Custom historical ranges do not refresh automatically.
The API's existing query deadlines and database reader limits still apply.

## Reproduce the query benchmark

Generate embedded assets as described in [CONTRIBUTING.md](../CONTRIBUTING.md), then:

```sh
go test ./internal/app -run '^$' -bench '^BenchmarkOverviewHistory$' -benchmem -benchtime=10x -count=3
```

The fixture writes 120,000 synthetic events across seven days, using 40 client
addresses, varied names and all six admitted outcomes. Range boundaries include
seconds and milliseconds. Setup writes are outside the timed loops. The fixture
validates its bounds and 119,988 in-window events before timing. Measurements
cover summary, rankings, and four simultaneous provider calls:
summary, rankings, timeseries, and performance. The concurrent measurement uses
the normal two-reader pool and checks every request for errors.

These are local provider timings, not HTTP or browser timings. The fixture has
no concurrent DNS writer or complete observation coverage. When comparing
revisions, use the same fixture and machine, run revisions sequentially, and
record the source revision, toolchain, hardware, and any uncommitted changes.
Report allocation changes alongside timings. Benchmark results from a developer
machine do not predict latency on a Raspberry Pi.
