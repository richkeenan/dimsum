# Benchmarks and performance fixtures

This directory contains developer tools for measuring DNS request handling,
query-history storage, and dashboard memory use. Use them when comparing a code
change or investigating performance. You do not need them to install or run
dimsum.

The fixtures use synthetic traffic and temporary local services. Run the commands
below from the **repository root**. See [CONTRIBUTING.md](../CONTRIBUTING.md) for
toolchain versions and initial setup, including generating the embedded web assets
before Go tests on a fresh checkout.

## Directory map

| Directory | Purpose | Entry point |
| --- | --- | --- |
| `workload/` | Generate repeatable DNS query sequences: a household-style mix, a repeated hot key, or unique-name churn. | Go package used by `runner/`. |
| `runner/` | Schedule queries at a fixed offered rate with bounded workers and queues; exercise the resolver and loopback network paths. | Go tests and `BenchmarkNetworkMixed`. |
| `manifests/` | Pin workload seeds, expected hashes, query-type counts, and correctness expectations. | [Fixture methodology](manifests/README.md) and `local-v1.json`. |
| `storage/` | Measure SQLite query-history batch writes and paginated reads. | `BenchmarkWriteBatch512` and `BenchmarkHistoryPage`. |
| `rules/` | Document synthetic policy-index fixtures and memory accounting. | [Policy benchmark instructions](rules/README.md); benchmark code lives in `internal/policy/`. |
| `ui/` | Observe a running dashboard and daemon over time with Chromium. | `soak.mjs`. |

## Start with correctness checks

```sh
go test ./bench/...
```

This checks the workload generator, scheduler, fixture manifests, and resolver
network behavior. Ordinary `go test` does **not** execute Go benchmarks. Passing
these checks establishes fixture correctness, not a performance result.

The runner sends queries on their scheduled arrival times rather than waiting
for previous replies. It counts queue overflow, timeouts, and failures separately.
Its latency includes time waiting in the queue. See the
[manifest README](manifests/README.md) for details and examples of using its Go API.

## Run Go benchmarks

### Resolver network path

```sh
go test ./bench/runner -run '^$' -bench '^BenchmarkNetworkMixed$' -benchmem -benchtime=10x
```

Each benchmark iteration schedules 100 UDP queries against a loopback fixture.
The output includes scheduled-arrival latency percentiles (`p50-us`, `p99-us`),
query counts, and failures. Go's `ns/op` and allocation metrics describe an entire
100-query iteration, not one DNS query. The fixture checks upstream activity;
this is a warmed local scenario, not a measurement of internet DNS latency.

### Query-history storage

```sh
go test ./bench/storage -run '^$' -bench . -benchmem -benchtime=100x
```

`BenchmarkWriteBatch512` writes 512 events per iteration and reports database
bytes per event and WAL size alongside Go's timing and allocation metrics.
Use a fixed iteration count when comparing runs because it determines the number
of stored events. `BenchmarkHistoryPage` reads 100 rows from a 4,096-event fixture.
Both use temporary databases on the local filesystem.

For policy lookup and index-build benchmarks, follow the
[rules README](rules/README.md). Those fixtures include up to one million rule
memberships and need more memory than the small examples above.

### Device policy selection and sharing

```sh
go test ./internal/config -run '^$' -bench 'BenchmarkClient(Selection|PolicyScaling)$' -benchmem -count=5
go test ./internal/resolve -run '^$' -bench 'BenchmarkWarm.*Pipeline$' -benchmem -count=5
```

The scaling fixture uses one synthetic 100,000-rule subscription with 1, 256 or
4,096 devices. Common policies share one matcher; distinct policies add one
custom rule per device and retain separate small matching views. The timed
operation selects the final device by IPv6 address and matches one subscribed
suffix. Compilation, normalization and GC are outside the timed loop.

`subscription-B` is the subscription index's structural memory accounting;
`view-retained-B` is the post-GC heap delta for compiling the owner views, with
the subscription and input configuration already resident. Small heap deltas
are noisy; these are neither peak allocation nor process RSS measurements.
`matchers` verifies the expected sharing count. Resolver warm benchmarks include
cache reconstruction and per-device filtering, but exclude sockets and statistics.

For a before/after comparison, run identical benchmark names and fixed inputs
from recorded clean source trees, sequentially without other qualification jobs.
Use several samples and report both latency and allocation changes. Features
absent in an older revision have no direct baseline; label their measurements
separately rather than substituting another workload.

## Run the dashboard soak test

Build a binary for the machine running the test using the
[documented GoReleaser workflow](../README.md#build-from-source). Install the web
dependencies and Playwright's Chromium browser as described in
[CONTRIBUTING.md](../CONTRIBUTING.md#browser-integration).

```sh
node bench/ui/soak.mjs /absolute/path/to/dimsum artifacts/ui-soak-example 60
```

The output directory must not already exist; its parent must exist. The final
argument is the duration in seconds, from 1 to 86,400, defaulting to 3,600.
The script needs `ps` and a working Chromium installation.

The script creates an isolated configuration, starts dimsum on ephemeral loopback
ports, signs into the dashboard, and sends synthetic blocked queries. It samples
daemon RSS, JavaScript heap size, DOM nodes, and document counts approximately
every five seconds while refreshing the dashboard. It stops its daemon and
browser when finished.

Look in the output directory for:

- `report.json`: completion status, query/sample counts, and errors.
- `memory.jsonl`: timestamped memory and browser measurements.
- `final.png`: a dashboard screenshot after a completed observation loop.
- The generated configuration, credentials, and service data.

Keep this output in ignored local storage such as `artifacts/`; do not commit it.

## Comparing results

Record the source commit, hardware, OS, toolchain, command, and raw output for each
run. Keep workload sizes and iteration counts consistent. Report failures and
timeouts with latency numbers, and distinguish allocations from retained memory
and process RSS. Synthetic local results do not establish performance on a
Raspberry Pi or a production network.

For the separate resolver qualification smoke test, see
[`guides/testing.md`](../guides/testing.md). Its workload and measurements differ
from the fixtures here.
