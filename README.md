<p align="center">
  <img src="web/src/assets/dimsum.svg" width="200" alt="dimsum’s happy lotus-leaf rice parcel mascot">
</p>

# dimsum

dimsum is a DNS ad blocker for your home or private network that you can manage
through your AI agent. Run it on a Linux server, Raspberry Pi, or Mac, point your
devices at it for DNS, and ask your agent to configure filtering, investigate
blocked domains, or manage local DNS. You can also use the web dashboard or CLI.

- Block ads and trackers with filter lists, custom rules, and allow rules.
- Configure local DNS records and inspect query history, clients, and upstream health.
- Back up and restore configuration.
- Control the running server from your agent through built-in HTTP MCP.
- Optionally provide DHCPv4 leases on Linux.

A single executable includes the dashboard. Configuration lives in a
comment-preserving YAML file; statistics, leases, and downloaded lists live separately.

## Run your DNS through your agent

Your agent can inspect and change dimsum's configuration through its built-in
[Model Context Protocol (MCP)](guides/agent-control.md) server. Manage filter
lists, block and allow rules, device names, local records, upstreams, settings,
and DHCP from the same conversation you use to troubleshoot your network.
Your agent uses the same control API as the dashboard and CLI.

Try asking:

> “Pause blocking for five minutes.”
>
> “Why is ads.example.com blocked? Show me the matching rule.”
>
> “Rename the device at 192.0.2.25 to Office laptop.”
>
> “Back up my configuration, then refresh my filter lists and check the result.”

### Connect your agent

1. Open **Settings → Agent access** and create a named token.
2. Add an **HTTP / Streamable HTTP MCP server** in your agent client using the
   displayed URL, such as `http://dns-server:8080/mcp`.
3. Set the header to `Authorization: Bearer YOUR_TOKEN`.

Tokens grant administrator access; revoke them in Settings. Protect their
transport just as you would a dashboard password.

See the [agent control guide](guides/agent-control.md) for setup, more example
requests, and how your agent checks that changes have taken effect.

## Install

On **64-bit Linux with systemd** (amd64 or arm64):

```sh
curl -fsSL https://raw.githubusercontent.com/richkeenan/dimsum/main/install.sh | sudo sh
```

The installer downloads the latest release, configures the service, and starts
it at boot. Open the dashboard URL it prints and sign in with **`admin`**.
You can change your password in **Settings**. Choose
subscriptions in **Filter lists**, then set your router's DNS server to this
machine's IP address.

The installer checks for conflicts on DNS port 53 and dashboard port 8080. See
the [deployment guide](guides/deployment.md) for port conflicts, containers,
manual setup, password recovery, and HTTPS. The default HTTP dashboard is for a
trusted private network; use HTTPS when credentials cross an untrusted network.

**Upgrade:** run the installer again. Existing configuration, credentials, and
history are preserved. For package-managed installations, upgrade using the
downloaded `.deb` instead.

[Download releases](https://github.com/richkeenan/dimsum/releases).

### Mac with Docker Desktop

Install and start Docker Desktop, then run:

```sh
mkdir -p ~/dimsum-docker
cd ~/dimsum-docker
curl -fL https://github.com/richkeenan/dimsum/releases/latest/download/compose.desktop.yaml -o compose.yaml
docker compose up -d --wait
```

Open <http://localhost:8080> and sign in with **`admin`**. DNS is published on
TCP/UDP port 53 for your LAN; the dashboard and MCP stay on localhost. Keep your
Mac awake and Docker Desktop running. Use your router for DHCP.

The Compose release asset and GHCR images will be published by a future tagged
release; pushing these changes alone does not publish them. The quickstart needs
that release to be available. See the [macOS guide](guides/macos.md) for port
conflicts, upgrades, backups, and the native executable option.

## Command-line control

Log in once as your normal user with the dashboard password (`admin` on a fresh
installation), then run commands without sudo:

```sh
dimsum login
dimsum control help
dimsum control diagnostics
dimsum control settings
```

For a remote server, use `dimsum login --server https://dns.example.net`.
The CLI saves a revocable token in your user configuration directory. Run
`dimsum logout` to revoke it and remove the saved connection. See the
[deployment guide](guides/deployment.md#command-line-login) for storage and
local recovery details. Authenticated HTTP clients can retrieve the OpenAPI
specification at `/api/v1/openapi.json`.

## Guides

- [Agent control: setup and example requests](guides/agent-control.md)
- [Deployment and password setup](guides/deployment.md)
- [macOS: Docker Desktop and native executable](guides/macos.md)
- [Configuration and supported settings](guides/configuration.md)
- [Encrypted upstream DNS](guides/encrypted-upstreams.md)
- [Optional DHCPv4](guides/dhcp.md)
- [Building and contributing](CONTRIBUTING.md)
- [Frontend development](web/README.md)
- [Security reporting](SECURITY.md)

## Build from source

Install Go **1.26.8**, Node **24.21.0** with npm, GoReleaser **2.18.2**, and Git.
On a **Linux amd64 or arm64** development machine:

```sh
goreleaser build --snapshot --clean --single-target --output dist/dimsum
./dist/dimsum version
```

On **macOS**, build a native executable for the current Mac:

```sh
goreleaser build --snapshot --clean --single-target --output dist/dimsum
./dist/dimsum version
```

GoReleaser supports Darwin **arm64** (Apple silicon) and **amd64** (Intel).
Native macOS archives contain an unsigned executable and a foreground example
configuration; there is no launchd service or native installer-script support.
Follow the [native macOS quickstart](guides/macos.md#native-macos-executable).
To build archives and packages from a clean recorded commit:

```sh
goreleaser release --snapshot --clean --skip=publish,docker
```

To cross-compile on macOS for a Linux host, select its target explicitly:

```sh
GOOS=linux GOARCH=arm64 goreleaser build --snapshot --clean --single-target --output dist/dimsum
```

Run that binary on a Linux arm64 host. Go tests and frontend development can also
run on macOS.
See [CONTRIBUTING.md](CONTRIBUTING.md) for tests and package builds.

## License

dimsum and its project artwork are licensed under the [MIT License](LICENSE).
[Copied UI components](web/THIRD_PARTY.md) retain their original notices.
Downloaded blocklists retain their publishers' terms.
