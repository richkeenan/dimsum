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

## Optional DHCPv4 (Linux)

DHCP is **disabled by default**. The **DHCP** dashboard page manages settings,
reservations, lease inspection and an explicit environment-check job. A saved
change is not proof of service: inspect **Desired**, **Applied**, the pending
generation and any error. DHCP failure leaves DNS and administration available.

### Prepare the network and service

Use a permanent/static IPv4 address on an up Ethernet-compatible LAN interface.
The configured subnet prefix must match that interface. dimsum advertises its
server IP as DNS: DNS must listen on that IP **port 53** or `0.0.0.0:53`, with
UDP and TCP reachable from clients. An IPv6-only wildcard is insufficient.
The gateway is your router, not dimsum. Use a domain such as `home.arpa`, not
`.local`. Account for static devices and existing DHCP leases when choosing a
pool; quiet ARP or DHCP discovery cannot prove an address is unused.

The DNS-only systemd unit retains only `CAP_NET_BIND_SERVICE`. To opt into DHCP,
install [deploy/dhcp-capabilities.conf](deploy/dhcp-capabilities.conf) as
`/etc/systemd/system/dimsum.service.d/20-dhcp.conf`. It adds **CAP_NET_RAW** and
**AF_PACKET** for interface-bound link-layer replies and ARP probes. AF_NETLINK
is already allowed for interface/address inspection. **CAP_NET_ADMIN is not
needed by the service**; configure interfaces separately through the host's
network manager. Review existing local unit overrides before installing it.

After adding or removing this privilege override, run `systemctl daemon-reload`
and `systemctl restart dimsum` once. Configuration toggles cannot grant a running
process new privileges. The restart also restarts DNS; normal DHCP enable/disable,
pool and reservation changes subsequently apply without a DNS restart.
The installer does not install this opt-in override or enable DHCP.
Native packages supply the inactive example in
`/usr/share/doc/dimsum/dhcp-capabilities.conf`; release archives include it under
`deploy/` for offline use.

Synthetic configuration example (replace documentation addresses for your LAN):

```yaml
dhcp:
  enabled: false
  interface: eth0
  server_ip: 192.0.2.2
  subnet: 192.0.2.0/24
  gateway: 192.0.2.1
  range_start: 192.0.2.100
  range_end: 192.0.2.199
  lease_seconds: 86400
  local_domain: home.arpa
  max_leases: 1024
  reservations:
    - id: lab-printer
      mac: '02:00:00:00:00:10'
      address: 192.0.2.20
      hostname: lab-printer
```

Incomplete network settings can be saved while disabled. Reservations use exactly
one MAC or hex client ID; addresses must be in the subnet and may be inside or
outside the dynamic pool. Reservation edits/removal do not revoke live leases.
Ownership conflicts can appear during application after a successful save:
inspect the error, address owner, expiry and held-until time before choosing a
different address. Interface, server IP, subnet and domain changes require saving
DHCP disabled and waiting for the applied disable first. Disabling preserves
durable ownership and removes dynamic DHCP DNS/name publication.

### Handoff, containers and rollback

Prepare the disabled configuration, permissions and DNS first. Run the explicit
environment check; the optional other-server check sends one DISCOVER, never a
REQUEST. It may be unavailable if a host DHCP client owns UDP/68. Neither check
proves firewall reachability or absence of another DHCP server.

For an authorized cutover, stop the router's DHCP service before enabling dimsum.
Use a non-overlapping pool or wait out prior grants; turning the old server off
does not revoke its clients' leases. Confirm applied running state, client
renewal, advertised router/DNS and DNS queries from the LAN. For rollback, disable
dimsum and wait for the applied disable before restoring the previous DHCP
server. That server must still avoid addresses in unexpired dimsum grants.

DHCP needs direct access to LAN broadcasts, ARP and link-layer unicast before a
client has an IP. Publishing UDP/67 through an ordinary NAT bridge is insufficient.
On native Linux, host networking or a deliberately configured LAN-facing network
namespace can provide this connectivity, with NET_RAW and NET_BIND_SERVICE and
the permanent server address visible inside that namespace. Mount persistent
data storage. Docker Desktop host networking is not equivalent to a native Linux
LAN interface. Isolated Linux container tests cover veth broadcast/direct replies,
ARP, real client acquisition and the two-capability service adapter; host-network
or macvlan deployments on a physical LAN have not been qualified by those tests.

### Lease persistence and backups

Lease ownership is separate operational state in **`<paths.data_dir>/dhcp`**:
preserve the **whole directory**, including its initialization marker and SQLite
sidecar files. Configuration backup/restore includes DHCP settings and reservations,
but **does not include or reset leases**. Keep the data directory across upgrades,
container replacement and disable/re-enable. The running supervisor pins its data
directory at startup; changing that path requires a planned stopped-service move.

For a host move, fully stop the old service, copy the entire lease-state directory
and configuration with correct ownership/permissions to the new host, and keep
the old server stopped. Never run two writers/servers from cloned ownership state.
A stale lease backup can omit newer grants and is not automatically safe. If
current state cannot be transferred, wait out all prior possible grants before
reusing addresses; do not delete an initialized directory to bypass recovery
errors. Trustworthy host time is required. Storage failure suspends service and
preserves ownership; correct the cause, then disable and re-enable to recover.

### CLI and agent parity

Every dashboard operation uses the shared `/api/v1/dhcp` operations. Read the
saved revision before each mutation; after a timeout, inspect state instead of
blindly replaying. MCP tools are `get_dhcp`, `update_dhcp`, `get_dhcp_status`,
`list_dhcp_leases`, `list_dhcp_reservations`, `add_dhcp_reservation`,
`update_dhcp_reservation`, `remove_dhcp_reservation`, plus `create_job`/`list_jobs`.

```sh
dimsum control dhcp
dimsum control dhcp-status
dimsum control patch dhcp '{"revision":"READ_SAVED_REVISION","edits":[{"path":["enabled"],"value":false}]}'
dimsum control dhcp-reservations
dimsum control add dhcp/reservations '{"revision":"READ_SAVED_REVISION","item":{"id":"lab-printer","mac":"02:00:00:00:00:10","address":"192.0.2.20"}}'
dimsum control patch dhcp/reservations/lab-printer '{"revision":"READ_SAVED_REVISION","edits":[{"path":["hostname"],"value":"lab-printer"}]}'
dimsum control delete dhcp/reservations/lab-printer '{"revision":"READ_SAVED_REVISION"}'
dimsum control dhcp-leases --query 'limit=100&state=bound'
dimsum control job '{"kind":"dhcp-check","input":{"probe_other_servers":false}}'
dimsum control jobs
```

Lease pages use opaque cursors (maximum 256 rows). On `lease_cursor_expired`,
restart without a cursor; changing a filter also starts a new page sequence.
Disabled/unavailable live inspection does not prove preserved ownership is empty.
There is deliberately no force-release operation.

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
