# Local deterministic fixtures

All automated networking uses loopback and OS-assigned test ports. It makes no
requests to public DNS, router services, or the existing Pi-hole. No production
DNS parsing library is imported.

## Lifecycle runner

```sh
go run ./cmd/dimsum validate -config testdata/config/dimsum.yaml
go run ./cmd/dimsum serve -config testdata/config/dimsum.yaml
```

The runner prints one JSON object with its actual DNS/admin addresses. UDP and
TCP DNS share the same assigned port. SIGINT/SIGTERM releases every listener.
This is a lifecycle skeleton: the JSON reports `ready: false`; handlers are a
later task. `validate` is offline and reports `active_available: false`.

Run the required focused suite or the full suite with race detection:

```sh
go test ./internal/config ./internal/app ./tests/integration -run 'TestConfig|TestLifecycle'
go test -race ./...
```

The module requests Go 1.26.8 through automatic toolchain selection. Start with
`GOTOOLCHAIN=auto` if the environment normally pins its installed Go version.

## Synthetic upstream

`internal/testutil.NewUpstream(clock, handler)` binds loopback UDP and framed TCP
on one ephemeral port. It exposes raw wire bytes without parsing DNS, so future
tests can send valid packets, malformed responses, or arbitrary recorded bytes.
The handler supplies a response, a simulated delay, or `Drop: true`. Closing the
fixture simulates an unavailable endpoint and joins its serving goroutines.

Create time with `NewClock(time.Unix(...))`. After receiving from
`upstream.Requests()`, the response timer is registered: `clock.Advance(duration)`
can release it deterministically. `Request.At` is simulated time. Always close
fixtures with `t.Cleanup`; shutdown also cancels delay timers and idle TCP reads.
The handler must return promptly and be safe for one UDP plus one TCP caller.
The fixture serializes each transport and bounds its observation queue at 64;
consume that queue. It is for correctness tests, not a throughput benchmark.

## Configuration experiment and chosen layout

`TestConfigFormatExperiment` compares YAML v3 AST and TOML document round trips:

```sh
go test ./internal/config -run TestConfigFormatExperiment -v
```

Both preserve comments but normalize unrelated whitespace. The selected format
is **one `dimsum.yaml` authoritative document**, with YAML v3 for strict decoding
and source-located edits instead of whole-document formatting. Current edits
support existing single-line scalar values in block mappings. Unsupported edits
return errors; they never silently serialize the whole file. Collections remain
directly editable by the operator; their structured edit operations come later.

`Document.Edit` validates all requested replacements together and preserves
every byte outside the replaced scalar tokens. `Publish` checks the expected
SHA-256 revision, writes/syncs a staging file in the same directory, checks the
revision again, renames once, and syncs the directory. A group is one file, never
a set of supposedly atomic renames. Abandoned staging files are not authoritative.
Mount the configuration directory, not just the file, in future containers.

Keep derived data under `paths.data_dir`, restricted secrets under
`paths.secrets_dir`, and authoritative YAML outside both. Relative paths currently
mean process working directory. Neither data nor secret directories are created
by this skeleton. Example paths are local fixture values, not installation paths.
