<p align="center">
  <img src="web/src/assets/dimsum.svg" width="200" alt="dimsum’s happy lotus-leaf rice parcel mascot">
</p>

# dimsum

dimsum is a DNS ad blocker for your home or private network that you can manage
through your AI agent. Run it on a Linux server or Raspberry Pi, point your
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

## Command-line control

On a native installation:

```sh
sudo -u dimsum dimsum control help
sudo -u dimsum dimsum control diagnostics
sudo -u dimsum dimsum control settings
```

The CLI uses a permission-protected Unix socket. Authenticated HTTP clients can
retrieve the OpenAPI specification at `/api/v1/openapi.json`.

## Guides

- [Agent control: setup and example requests](guides/agent-control.md)
- [Deployment and password setup](guides/deployment.md)
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

On macOS, select a Linux target explicitly, for example:

```sh
GOOS=linux GOARCH=arm64 goreleaser build --snapshot --clean --single-target --output dist/dimsum
```

Run that binary on a Linux arm64 host. Go tests and frontend development can run
on macOS; the release configuration does not produce a native macOS executable.
See [CONTRIBUTING.md](CONTRIBUTING.md) for tests and package builds.

## License

dimsum and its project artwork are licensed under the [MIT License](LICENSE).
[Copied UI components](web/THIRD_PARTY.md) retain their original notices.
Downloaded blocklists retain their publishers' terms.
