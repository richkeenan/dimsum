# Compact policy index fixtures

Fixtures are generated deterministically by `benchmarkRules` in
`internal/policy/index_bench_test.go`; no million-line checkout or network access
is required. Sizes: 80,000 / 250,000 / 1,000,000 memberships. Names follow
`ad%07d.zone%03d.test`, eight stable source IDs, unique stable rule IDs, and
verbatim source text. Snapshot/build/reference fixtures are half exact and half
suffix. Exact candidates use the same owners as exact keys. Suffix candidates
compare hash-and-suffix-walk against reversed-label sorted lookup on 1,024
rotating queries (half descendant hits, half misses). The full working-set
benchmark rotates 2,048 queries mixing apex hits, descendant hits and misses.

```sh
go test ./internal/policy -run '^$' -bench . -benchmem -benchtime=100ms -count=10
```

The short benchmark duration keeps repeated million-rule builds practical.
`BenchmarkSnapshotBuild` includes the active snapshot, input, new snapshot and
build garbage; its 1 ms heap sampler reports an observed peak lower bound.
`active-new-input-heap-B` is after GC with both snapshots and input still live.
Go's B/op is total allocation, **not** retained memory. Index-only candidate
bytes exclude provenance; full snapshot/build accounting includes provenance.
Hash seeds vary per build. Hot-name repetitions share the parent-built index;
working-set and build repetitions construct new indexes. Lookup loops contain
no assertions.

These synthetic fixtures compare representations, not publisher distributions,
Pi performance, process RSS limits, or release qualification. Publish hardware,
toolchain, repetition ranges, accounting versus measured heap, and remaining
targets alongside captured results before making performance claims.

See [the measured candidate decision](results-2026-09-21.md).
