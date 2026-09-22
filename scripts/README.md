# Build and installation helpers

| Script | Caller | Purpose |
|---|---|---|
| `build-web.sh` | `.goreleaser.yaml`, CI | Install pinned frontend dependencies and generate the embedded UI. |
| `artifact-metadata.mjs` | `.goreleaser.yaml`, `deploy/Dockerfile` | Record build inputs and collect dependency license notices for packages. |
| `source-inputs.mjs` | `artifact-metadata.mjs` | Hash tracked source inputs and report checkout state without including ignored files. |
| `install-service.sh` | `install.sh` | Install a verified release archive and roll back a failed upgrade. |
| `qualification.mjs` | `.github/workflows/local-artifacts.yaml` | Run a bounded DNS smoke test with isolated loopback fixtures. |
| `dhcp-qualification.py` | Developer | Snapshot current sources and run offline Linux DHCP qualification with JSON and Markdown evidence. |

See [CONTRIBUTING.md](../CONTRIBUTING.md) for build prerequisites and package
commands. Run the source-input boundary tests with `node --test scripts/*.test.mjs`.

## Automated DHCP qualification

Generate current embedded web assets, then choose a new ignored output directory:

```sh
sh scripts/build-web.sh
python3 scripts/dhcp-qualification.py --output docs/dhcp-qualification/run-001
```

Requires Python 3.9+ and a Linux Docker engine. The runner builds
`dhcp-qualification.Dockerfile` using `golang:1.26.8-bookworm`, installs iproute2,
BusyBox, procps and util-linux, and downloads/verifies the modules locked by
`go.mod`/`go.sum`. Image preparation needs network access; every test runs with
`--network none`, offline Go module settings, no mounts, and only NET_ADMIN,
NET_RAW, NET_BIND_SERVICE, SYS_ADMIN and SETPCAP capabilities. Unconfined seccomp permits
disposable child network namespaces. Fixture sysctls affect only the container.
The Go version is checked before tests. Debian package versions and the image ID
are recorded; the base tag and apt repositories are not digest/snapshot pinned.

The snapshot includes tracked and non-ignored untracked files plus generated
`internal/webassets/dist/` assets, with SHA-256 inputs recorded. It excludes ignored
private files. Generate assets after frontend changes and avoid editing sources
during snapshot creation. Each package runs serially (`-p 1`, `-count=1`), followed
by opted-in Linux DHCP/app tests with raw sockets, BusyBox, and child namespaces.
Capability-drop testing uses SETPCAP and a privileged-port threshold of 1024.
Resource qualification (`TestQualificationStatePlateau` and
`TestDHCPQualificationCoexistence`) is explicitly deferred; this runner makes no
resource or coexistence measurement claims. Browser opt-ins are skipped.

Independent package/phase tests continue after failures. Evidence includes raw
Go test JSONL, stderr, build/preflight logs, commands, durations, commit and dirty
status, failing/skipped test names, `report.json`, and `summary.md`. Output must be
a new ignored directory beneath `docs/`; existing runs are never overwritten.
Exit status is nonzero on failed tests or blocked prerequisites. PASS means the
enabled tests passed, not that skipped qualifications or live/Pi testing passed.
