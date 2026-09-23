# Integration tests

See [CONTRIBUTING.md](../CONTRIBUTING.md) for the standard Go and frontend checks.
Test fixtures use synthetic names, loopback addresses, and disposable containers.

## Resolver and container smoke tests

CI runs the maintained Node integration tests in `tests/`:

```sh
node --test tests/*.test.mjs
node tests/qualification.mjs dist/dimsum --output docs/resolver-smoke --duration 30
node tests/container-smoke.mjs dimsum:smoke
```

Build the executable with the GoReleaser commands in the root README. The resolver
test requires a new output directory and runs local DNS/upstream fixtures. Its
optional reload and outage flags are exercised in `.github/workflows/local-artifacts.yaml`.
The container test takes an already-built image; see the
[deployment guide](deployment.md#containers) for image build commands.

## Isolated Linux DHCP tests

Use a Linux Docker engine, including Docker Desktop on macOS. Generate the UI,
then build the test image from the repository root:

```sh
sh scripts/build-web.sh
docker build -f tests/dhcp.Dockerfile -t dimsum-dhcp-test .
```

The Dockerfile-specific allowlist includes Go test sources and embedded assets,
and excludes private checkout files. Image preparation needs network access for
the Go image, Debian tools, and locked Go modules. The base tag and Debian package
repositories are not digest/snapshot pinned.

Run the ordinary package tests offline:

```sh
docker run --rm --network none --cap-drop ALL dimsum-dhcp-test \
  go test -count=1 -p 1 -timeout 10m \
  ./internal/dhcp ./internal/app ./internal/config ./internal/control \
  ./internal/admin ./internal/webassets
```

Run the Linux adapter tests with their explicit opt-ins and isolated namespace
settings. Keep `--network none`; these fixtures create interfaces and child
network namespaces inside the disposable container.

```sh
docker run --rm --network none --cap-drop ALL \
  --cap-add NET_ADMIN --cap-add NET_RAW --cap-add NET_BIND_SERVICE \
  --cap-add SYS_ADMIN --cap-add SETPCAP \
  --security-opt seccomp=unconfined \
  --sysctl net.ipv4.conf.default.accept_local=1 \
  --sysctl net.ipv4.conf.all.rp_filter=0 \
  --sysctl net.ipv4.conf.default.rp_filter=0 \
  --sysctl net.ipv4.ip_unprivileged_port_start=1024 \
  -e DIMSUM_DHCP_ISOLATED_TEST=1 -e DIMSUM_DHCP_UDHCPC_TEST=1 \
  -e DIMSUM_DHCP_NETNS_TEST=1 -e DIMSUM_DHCP_CAPABILITY_TEST=1 \
  dimsum-dhcp-test go test -v -count=1 -p 1 -timeout 10m \
  -run '^TestLinux' ./internal/dhcp ./internal/app
```

Review the verbose output for skipped tests as well as failures. A successful
command does not establish that skipped qualifications passed. These commands do
not enable resource/coexistence stress tests, browser scenarios, or live-network
tests. Resource fixtures use `DIMSUM_DHCP_QUALIFY=1` with
`TestQualificationStatePlateau` and `TestDHCPQualificationCoexistence`; inspect
their test definitions for workload parameters. Keep local logs and reports in
ignored `docs/` output.

## Terminal login test

`go test ./cmd/dimsum -run TestLoginPromptCancellationRestoresTerminal -count=1`
uses a Go pseudo-terminal fixture on Linux and macOS. It checks hidden password
entry, terminal restoration, and cancellation through signals and Ctrl-C.
