# Configuration

dimsum uses one authoritative YAML document. The UI, CLI, HTTP API, and MCP tools
edit the same configuration through revision-checked operations. Downloaded list
artifacts, query statistics, and DHCP leases live outside that document.

## Minimal local development configuration

This example binds local high ports and forwards to a public DNS resolver. Choose
an upstream suitable for your network before running it:

```yaml
version: 1
dns:
  listen: [127.0.0.1:5353]
  upstreams: [1.1.1.1:53]
admin:
  listen: 127.0.0.1:8080
  allowed_hosts: [127.0.0.1:5173]
paths:
  data_dir: data
  secrets_dir: secrets
```

Store this in a private temporary directory. Relative paths resolve against the
configuration file's directory. `allowed_hosts` admits the local Vite development
proxy; omit that entry for ordinary deployment. See [deployment.md](deployment.md)
for credential bootstrap and HTTPS.

Validate before starting:

```sh
dimsum validate -config /path/to/dimsum.yaml
dimsum serve -config /path/to/dimsum.yaml -state /path/to/recovery
```

## Settings reference

| Section | Purpose |
| --- | --- |
| `dns` | Listeners, primary/fallback upstreams, and forwarding limits. |
| `admin` | Dashboard listener, allowed hosts, secure cookies, and local control socket. |
| `paths` | Derived data and restricted secret directories. |
| `cache` | Cache capacity and stale-answer behavior. |
| `rules` | Explicit filtering and allow rules. |
| `lists` | Downloaded lists, parser dialects, and enabled state. |
| `filtering` | Policy behavior and special-domain handling. |
| `records`, `zones` | Local DNS answers and local zones. |
| `clients`, `naming` | Explicit client names and discovery sources. |
| `statistics` | Query history and retention settings. |
| `dhcp` | Optional DHCPv4 settings and reservations. |

Use **Settings**, `dimsum control settings`, or the
[OpenAPI contract](../api/openapi.yaml) for supported fields and edit schemas.
The YAML structures live in `internal/config/config.go`; validation reports
unsupported values and incompatible combinations. The
[native example](../deploy/dimsum.example.yaml) provides installation paths and
wildcard listeners. DHCP has a [separate guide](dhcp.md).

## Applying changes

Read `saved_revision` before a mutation and send it with the requested edits.
If another writer changes the document, reload and review the current values
before retrying. Saving configuration and activating it are distinct operations:
inspect the active generation, pending state, and errors after saving.

Listener, secure-cookie, and runtime path changes can require a restart. Ordinary
rules, client names, and supported settings apply through the coordinator.
Preserve the configuration directory's ownership and permissions. Avoid mounting
only the file, since atomic saves replace its directory entry.
