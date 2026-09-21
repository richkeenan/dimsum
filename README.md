<p align="center">
  <img src="web/src/assets/dimsum.svg" width="200" alt="dimsum’s happy lotus-leaf rice parcel mascot">
</p>

# dimsum

dimsum is a DNS ad blocker for your home or private network. Run it on a Linux
server or Raspberry Pi, point your devices at it for DNS, and manage filtering
through a web dashboard, CLI, or your AI agent.

- Block ads and trackers with filter lists, custom rules, and allow rules.
- Configure local DNS records and view query history, clients, and upstream health.
- Back up your configuration or import a Pi-hole setup.
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

[Downloads](https://github.com/richkeenan/dimsum/releases) ·
[Other installation methods and troubleshooting](docs/operations/install.md) ·
[Pi-hole migration](docs/operations/migration.md)

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

See the [CLI and agent reference](docs/operations/agent-control.md) for configuration
changes, API access, and automation.

## Development

See the [development guide](docs/operations/development.md) for source builds,
running locally, frontend live reload, and tests. Contributors use GoReleaser;
users install prebuilt releases.

## Documentation

- [Installation and upgrades](docs/operations/install.md)
- [Configuration, filtering, and local DNS](docs/05-filtering-and-configuration.md)
- [Backups, recovery, and rollback](docs/operations/recovery.md)
- [Pi-hole migration](docs/operations/migration.md)
- [Architecture](docs/01-product-and-architecture.md)
- [Implementation status and measured limits](docs/implementation-status.md)

The project license has not yet been selected. See the development guide for
dependency notices and build metadata.
