<p align="center">
  <img src="web/src/assets/dimsum.svg" width="200" alt="dimsum’s happy lotus-leaf rice parcel mascot">
</p>

# dimsum

dimsum is a DNS ad blocker for your home or private network. Run it on a Linux
server or Raspberry Pi, point your devices at it for DNS, and manage filtering
through a web dashboard, CLI, or your AI agent.

- Block ads and trackers with filter lists, custom rules, and allow rules.
- Configure local DNS records and view query history, clients, and upstream health.
- Back up and restore your configuration.
- Manage the same server through the browser, CLI, JSON API, or HTTP MCP.

A single executable includes the dashboard. Configuration lives in a
comment-preserving YAML file; statistics and downloaded lists live separately.

## Install

On a **64-bit Linux server or Raspberry Pi with systemd**:

```sh
curl -fsSL https://raw.githubusercontent.com/richkeenan/dimsum/main/install.sh | sudo sh
```

The installer downloads the latest release, sets up dimsum, and starts it at boot.
Open the dashboard URL it prints, sign in with **`admin`**, and change your password
in **Settings**. Choose subscriptions in **Filter lists**, then set your router’s
DNS server to this machine’s IP address.

**Upgrade:** run the same command again. Your settings, password, and history stay
in place.

[Download releases](https://github.com/richkeenan/dimsum/releases).

## Connect your agent

1. Open **Settings → Agent access** and create a named token.
2. Add an **HTTP / Streamable HTTP MCP server** in your agent client using the
   displayed URL, such as `http://dns-server:8080/mcp`.
3. Set the header to `Authorization: Bearer YOUR_TOKEN`.

Ask “Which devices made the most DNS requests in the last hour?” or “Why is this
domain blocked?” Your agent can inspect traffic and manage configuration through
typed tools. Tokens grant administrator access; revoke them in Settings.

## Command-line control

On the server:

```sh
sudo -u dimsum dimsum control help
sudo -u dimsum dimsum control diagnostics
sudo -u dimsum dimsum control settings
```

Run `dimsum control help` for available commands. The server exposes its OpenAPI
specification at `/api/v1/openapi.json` to authenticated clients.

## Development

Install Go **1.26.8**, Node **24.21.0** with npm, GoReleaser **2.18.2**,
and Git. Build from the repository root:

```sh
goreleaser build --snapshot --clean --single-target --output dist/dimsum
./dist/dimsum version
```

GoReleaser builds the frontend before embedding it in the executable. Git ignores
the generated bundles. Before running Go tests on a fresh checkout, generate them:

```sh
sh scripts/build-web.sh
go test ./...
go vet ./...
npm --prefix web test
```

See [web/README.md](web/README.md) for frontend development and browser tests.
Use `goreleaser release --snapshot --clean --skip=publish,docker` to build local
packages. The packaging hooks write
dependency notices and build metadata under `artifacts/packaging-metadata/`.

## License

dimsum is licensed under the [MIT License](LICENSE).
