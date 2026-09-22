# Build and installation helpers

| Script | Caller | Purpose |
|---|---|---|
| `build-web.sh` | `.goreleaser.yaml`, CI | Install pinned frontend dependencies and generate the embedded UI. |
| `artifact-metadata.mjs` | `.goreleaser.yaml`, `deploy/Dockerfile` | Record build inputs and collect dependency license notices for packages. |
| `install-service.sh` | `install.sh` | Install a verified release archive and roll back a failed upgrade. |
| `qualification.mjs` | `.github/workflows/local-artifacts.yaml` | Run a bounded DNS smoke test with isolated loopback fixtures. |
