# Local open-loop fixtures, generator v1

`local-v1.json` pins three synthetic traces. Each hash is SHA-256 of the generated
UTF-8 records, in index order, using this exact format (including trailing LF):

```text
index<TAB>scheduled_offset_nanoseconds<TAB>absolute_name<TAB>numeric_qtype<LF>
```

The hashes were calculated independently with Python integer arithmetic and
checked against the Go generator. Changing generation semantics requires a new
version and reviewed hashes; tests must not regenerate expected hashes.

- **Household:** exactly 67 A, 14 AAAA, 17 HTTPS, one PTR and one SVCB per 100
  arrivals, permuted by seed. Approximately 80% of names select eight hot keys;
  the remainder select 256 other keys. This is synthetic skew, not measured
  household frequency, meaningful reverse lookups, or a burst replay.
- **Hot-key:** one A key repeated, useful for cache/coalescing experiments.
- **Churn:** unique A keys with seeded mixed labels and a unique index suffix.

The manifest's answer/upstream expectations apply to the **sequential preflight**
against the specified uncacheable, zero-record NOERROR fixture. These are not
cache-hit or coalescing expectations. The concurrent churn check also expects
exactly 100 upstream requests. All DNS traffic uses an ephemeral loopback port.
The fixture uses no EDNS, no loss/delay, and no answer records; packet sizes and
QTYPE mix are synthetic. No public DNS or household configuration is required.

## Run correctness gates

```sh
go test ./bench/workload ./bench/runner
go test -race ./bench/workload ./bench/runner
go vet ./bench/workload ./bench/runner
```

`TestLocalResolverManifest` validates hashes, QTYPE counts, client ID/question,
rcode, empty record sets, answer counts, and actual upstream exchanges before
running the open-loop churn smoke check. The oracle parsing and allocations in
this test are validation overhead; its durations are not performance results.

## Reuse the driver

Construct a `workload.Spec`, pass it to `workload.New`, then call `runner.Run`
with bounded workers, queue capacity, arrival-relative timeout, and a handler
factory. The factory creates worker-owned buffers before scheduling starts.
Handlers must honor their context and return an error on invalid replies.
`bench/runner/resolver_test.go` shows wiring to the existing resolver.

Arrival `i` is always scheduled at `floor(i * 1 second / Rate)`. Generation and
scheduler delay count toward latency. Full queues drop that arrival and increment
`overflow`; they do not postpone it or lower the offered count. Overdue arrivals
are offered immediately against the original schedule. There is no per-query
goroutine and no unbounded pending queue.

Each accepted sample records completion minus **scheduled arrival** as latency,
and time inside the handler as service time. Requests that expire while queued
have zero service time and still record latency. Overflow and not-offered entries
have no timing observations (zero timing fields except Scheduled). Report their
counts separately; never mix their zeros into latency percentiles. Successful
latency percentiles must also accompany timeout, failure, and overflow counts.
Run cancellation stops offering and marks the remainder `not-offered`; it returns
the partial report and the context error. Samples are retained in index order.

Limits: 100,000 samples, 256 workers, 100,000 queued entries, ten-minute schedule,
and at most one-minute arrival-relative timeout. Memory is O(count + queue +
workers); the trace generator retains only its spec. Runtime is bounded by the
schedule plus timeout **only when the supplied handler honors cancellation**.
The driver cannot forcibly stop arbitrary Go callbacks.

The exported Go API and `go test` commands are the entry points for this driver.
Use `scripts/qualification.mjs` for the separate bounded resolver smoke test;
its workload and measurements are not interchangeable with these manifests.
