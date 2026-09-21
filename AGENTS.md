# dimsum development

- Work directly on `main` in this checkout. Do not create worktrees or feature branches unless the owner asks.
- Make small, focused commits with meaningful verification. Never force-add ignored agent reports or scratch files.
- Use `github.com/stretchr/testify/require` for test prerequisites and `assert` for independent checks. Call `require` only from the test goroutine; send worker errors back to the test. Keep assertions outside benchmark/allocation measurement closures.
- Follow the specifications in `docs/`; `docs/11-agent-control-and-deployment.md` defines agent-first configuration and deployment. Production DNS handling is hand-built; DNS libraries are test oracles only.
- Keep existing Pi-hole and production network settings untouched. Tests use isolated local ports and fixtures. Cutover and public release require owner authorization.
- Every UI operation must have a CLI equivalent. Configuration is authoritative Git-friendly text; derived state and statistics are separate.
- Consult current library documentation for dependencies. Do not claim Pi benchmarks, multi-day soaks, or release qualification without actual evidence.
- Build dimsum binaries with GoReleaser, using `.goreleaser.yaml` as the single source of build targets, compiler flags, and version metadata. Do not run `go build` directly or duplicate build flags in scripts. Use `goreleaser build --snapshot --clean --single-target --output dist/dimsum` for local development, `sh scripts/build-local.sh` for packages, and `sh scripts/deploy-pi.sh HOST HEALTH_URL` for an owner-authorized Pi upgrade. Deploy a recorded committed build; use a clean clone if this checkout contains concurrent work.
- Never commit real household device names, IP/MAC addresses, serial numbers, or raw network inventories. Use synthetic names/models and documentation address ranges in fixtures and examples; keep live probe output outside tracked files.
