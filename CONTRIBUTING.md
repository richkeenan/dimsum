# Contributing to dimsum

Start with a focused issue or pull request describing the behavior you want to
change. Include reproduction steps for bugs and explain how you verified a fix.
Send security reports through the [private reporting route](SECURITY.md).

## Development environment

Use Go 1.26.8, Node 24.21.0 with npm, and GoReleaser 2.18.2. The Go module declares
its toolchain; frontend versions and the transitive dependency graph are pinned
in `web/package.json` and `web/package-lock.json`.

Generate the embedded frontend before running Go tests on a fresh checkout:

```sh
sh scripts/build-web.sh
npm --prefix web run lint
npm --prefix web run fmt:check
go test ./...
go vet ./...
npm --prefix web test
node --test tests/*.test.mjs
```

Run `go test -race ./...` for changes involving concurrency. Linux-only DHCP
adapters need Linux tests; [the testing guide](guides/testing.md) describes the
isolated container harness. Tests that send LAN discovery traffic require
explicit environment opt-ins. Use synthetic names and documentation addresses
in fixtures, and keep live network captures out of commits and CI artifacts.

## Browser integration

```sh
npm --prefix web exec -- playwright install chromium
npm --prefix web run test:e2e
DIMSUM_BROWSER_TEST=1 go test ./internal/webassets -run 'TestBrowserAgainst(GoAPI|ManagedRuntime)$' -v -count=1
```

Fixture browser tests exercise UI states using intercepted responses. The two
Go-hosted scenarios check the real HTTP/session/configuration boundary and a
managed server with local DNS and SQLite. Release CI runs both modes.
See [web/README.md](web/README.md) for the frontend workflow.

## Source layout

- `cmd/dimsum/`: command entry points and offline bootstrap.
- `internal/`: DNS, policy, storage, configuration, and administration packages.
- `api/openapi.yaml`: shared HTTP contract.
- `web/`: React dashboard and generated API/route types.
- `tests/`: browser and cross-package integration tests.
- `deploy/`: service configuration and required packaging code.
- `scripts/`: only the frontend-build and release-install entry points.
- `bench/`: synthetic workloads and reproducible benchmark methodology.

Configuration is authoritative text. Preserve comments and unrelated bytes when
editing it. Administration features should expose the same operation through
the UI, CLI, and shared API. Production DNS parsing is hand-built; use DNS
libraries as test oracles only.

## Building packages and images

Use GoReleaser as the source of build targets, flags, and executable version
metadata. See the root README for Linux builds and macOS cross-compilation.

```sh
goreleaser check
goreleaser release --snapshot --clean --skip=publish,docker
```

The release hooks build the frontend and collect project/dependency notices in
`artifacts/packaging-metadata/`. Go notices cover the whole module graph so
cross-platform dependencies retain their notices. Before distributing an
artifact, inspect its license files and metadata as well as its executable.

Build distributions from a clean recorded commit. Git-based metadata hashes
tracked files and reports untracked or modified inputs as dirty. Container build
contexts exclude Git history and private checkout files. Build arguments label
provenance; they do not authenticate the source contents. Source-archive builds
report unknown cleanliness and unrecorded provenance unless supplied explicitly.
See [deployment.md](guides/deployment.md#containers) for container commands.

## Documentation and generated files

Public guides belong in `guides/`. The ignored `docs/` directory is available
for private maintainer notes. Keep local credentials, build output, screenshots,
and agent scratch reports out of commits.

Python files, embedded Python, and Python tooling dependencies are not accepted.
Use the existing Go, Node, and shell toolchains. `scripts/` is limited to
`build-web.sh` and `install-service.sh`. Put maintained integration tests in
`tests/` and required packaging code in `deploy/`; prefer documented commands
over new wrappers. One-off investigation scripts and report generators belong
in ignored scratch space, not the public source tree.

Commit OpenAPI and route-tree generated types with their source changes. Embedded
web bundles are generated during builds and remain ignored. Explain benchmark
hardware, source revision, inputs, and methodology alongside performance results.
