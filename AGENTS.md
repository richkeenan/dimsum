# Contributor instructions

- Read `CONTRIBUTING.md` for build and verification commands. Read `AGENTS.local.md`
  when present for checkout-specific instructions; keep that ignored file private.
- Make focused changes and commits with verification appropriate to the behavior.
  Preserve unrelated work. Never force-add ignored reports, state, or credentials.
- Use `github.com/stretchr/testify/require` for test prerequisites and `assert`
  for independent checks. Call `require` only from the test goroutine and pass
  worker errors back to it. Keep assertions outside allocation measurement closures.
- Production DNS parsing is hand-built; DNS libraries are test oracles only.
  Use isolated ports and synthetic names, models, and documentation addresses in
  tests. Keep household inventories, credentials, and captures out of source control.
- Configuration is authoritative text. Preserve comments and unrelated bytes.
  Every UI operation must have a CLI equivalent through the shared control API.
- Consult current documentation when changing dependency integrations. Report
  actual verification results and identify skipped platform or integration checks.
- Build binaries with GoReleaser. `.goreleaser.yaml` owns targets, compiler flags,
  and version metadata; do not duplicate them with direct `go build` commands.
  Use the platform-specific commands in `README.md` and build distributions from
  a clean recorded commit.
- Generate embedded assets with `sh scripts/build-web.sh` before Go tests on a
  fresh checkout or after frontend changes. `internal/webassets/dist/` stays
  ignored. Keep screenshots and local build evidence out of commits.
- Public documentation belongs in `guides/`. `docs/`, `superpowers/`, and
  `.superpowers/` are ignored locations for local notes and agent scratch output.

## Running-instance administration

Use the dimsum MCP tools first for device names, blocking, rules, records,
settings, and diagnostics. Read the current revision before a mutation and read
back the saved result and activation status. Use SSH, CLI, or direct files when
the owner requests that method or MCP cannot perform the operation; explain the
limitation before falling back.

After updating a server, reconnect its MCP connection to refresh tool schemas.
If structured-result validation fails, compare the advertised schema with the
returned data before changing configuration or bypassing validation.
