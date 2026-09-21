# dimsum development

- Work directly on `main` in this checkout. Do not create worktrees or feature branches unless the owner asks.
- Make small, focused commits with meaningful verification. Never force-add ignored agent reports or scratch files.
- Use `github.com/stretchr/testify/require` for test prerequisites and `assert` for independent checks. Call `require` only from the test goroutine; send worker errors back to the test. Keep assertions outside benchmark/allocation measurement closures.
- Follow the specifications in `docs/`; `docs/11-agent-control-and-deployment.md` defines agent-first configuration and deployment. Production DNS handling is hand-built; DNS libraries are test oracles only.
- Keep existing Pi-hole and production network settings untouched. Tests use isolated local ports and fixtures. Cutover and public release require owner authorization.
- Every UI operation must have a CLI equivalent. Configuration is authoritative Git-friendly text; derived state and statistics are separate.
- Consult current library documentation for dependencies. Do not claim Pi benchmarks, multi-day soaks, or release qualification without actual evidence.
