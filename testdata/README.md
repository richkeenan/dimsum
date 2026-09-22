# Test fixtures

This directory holds small synthetic configuration and parser inputs. Normal Go
tests create temporary state and bind loopback ports chosen by the OS. Explicit
LAN discovery tests and Linux namespace/capability tests have separate opt-ins.

## Configuration fixture

`config/dimsum.yaml` uses port zero for isolated listeners. Tests supply their own
upstreams and temporary state. To experiment with a running server, use the
[development configuration](../guides/configuration.md) in a temporary directory
and sign in with `admin` on first startup. Do not store real credentials or query history here.
Relative state and secret paths resolve against the configuration directory.

## Synthetic upstream

`internal/testutil.NewUpstream(clock, handler)` binds loopback UDP and TCP on one
ephemeral port. The handler receives raw wire bytes and returns a response,
simulated delay, or `Drop: true`. Close the fixture with `t.Cleanup` to release
listeners and join its goroutines.

Create a deterministic clock with `NewClock(time.Unix(...))`. After reading a
request from `upstream.Requests()`, use `clock.Advance(duration)` to release its
response timer. The handler must honor cancellation and support concurrent
UDP/TCP calls. Consume the bounded observation queue. This fixture serves
correctness tests rather than throughput benchmarks.

## Parser inputs

[`lists/`](lists/README.md) contains authored hosts/adblock syntax fixtures.
[`internal/dnswire/testdata/dns/`](../internal/dnswire/testdata/dns/README.md)
documents hand-encoded packets and their expected byte offsets. Fuzz regression
inputs live with the package that exercises them.

Run focused checks after generating embedded web assets:

```sh
go test ./internal/config ./internal/lists ./internal/catalog ./internal/dnswire
go test ./internal/app ./tests/integration
```

See [CONTRIBUTING.md](../CONTRIBUTING.md) for the full suite and browser integration.
