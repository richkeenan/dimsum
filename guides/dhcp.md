# Optional DHCPv4 on Linux

DHCP is disabled by default. The **DHCP** dashboard page manages settings,
reservations, leases, and an explicit environment check. It suggests settings
from the server's IPv4 interface and default route, with a 24-hour lease and
`home.arpa` domain. Review those suggestions before saving; ambiguous networks
require manual entry.

## Network and service preparation

Use a permanent IPv4 address on an active Ethernet-compatible LAN interface.
The subnet must match that interface. dimsum advertises its server address as
DNS, so UDP and TCP DNS must be reachable on that address at port 53. An IPv6-only
wildcard listener is insufficient. The gateway is your router, not dimsum.

Choose a pool that excludes manually assigned addresses and accounts for leases
or reservations on your existing DHCP server. The suggested range excludes
detected local addresses and dimsum reservations, but cannot discover all prior
grants. Use a local domain such as `home.arpa`; avoid `.local`, which is used by mDNS.

dimsum probes new candidate addresses three times over 1.5 seconds before offering
them and quarantines conflicts. Unexpired leases retain ownership without a probe
response. A quiet ARP probe is not proof that an address is unused.

### Linux capabilities

The DNS-only systemd unit retains NET_BIND_SERVICE. To enable DHCP, install
[`deploy/dhcp-capabilities.conf`](../deploy/dhcp-capabilities.conf) as
`/etc/systemd/system/dimsum.service.d/20-dhcp.conf`, after reviewing any existing
unit overrides. The example adds NET_RAW and AF_PACKET for ARP and link-layer
replies. NET_ADMIN is not required by the service; configure the interface through
the host's network manager.

```sh
sudo systemctl daemon-reload
sudo systemctl restart dimsum
```

This restart also restarts DNS. Subsequent supported DHCP configuration changes
apply without restarting DNS. Enabling DHCP in the UI cannot grant new privileges
to a running process. The installer does not install this opt-in override.
Debian packages include it at `/usr/share/doc/dimsum/dhcp-capabilities.conf`;
release archives include it under `deploy/`.

## Configuration example

Replace these documentation addresses with your network's settings:

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

Incomplete settings can be saved while DHCP is disabled. Reservations identify
a device by exactly one MAC or hex client ID, and use addresses inside the subnet.
They may be inside or outside the dynamic pool. Editing a reservation does not
revoke a live lease; inspect ownership conflicts before choosing another address.

Disable DHCP and wait for the applied disable before changing the interface,
server address, subnet, or domain. Disabling preserves lease ownership and removes
DHCP-derived DNS answers and names.

## Handoff and rollback

Prepare configuration, permissions, and DNS first. Run the environment check.
Its optional other-server probe sends one DISCOVER, never a REQUEST; it may be
unavailable if a host DHCP client owns UDP port 68. The check cannot prove firewall
reachability or the absence of another DHCP server.

Stop the prior DHCP server before enabling dimsum. Use a non-overlapping pool or
wait out prior grants: stopping a server does not revoke its leases. After saving,
check **Desired**, **Applied**, pending generation, and errors. Confirm client
renewal, advertised gateway/DNS, and DNS resolution from the LAN. DHCP failure
leaves DNS and administration available.

To roll back, disable dimsum DHCP and wait for the applied disable before restoring
the prior server. Its pool must avoid addresses covered by unexpired dimsum leases.

## Containers

DHCP needs LAN broadcasts, ARP, and link-layer replies before a client has an IP.
Publishing UDP port 67 through a NAT bridge is insufficient. Native Linux host
networking or a LAN-facing network namespace can provide connectivity, with
NET_RAW and NET_BIND_SERVICE and a permanent server address visible inside the
namespace. Mount persistent data storage. Docker Desktop is not equivalent to a
native Linux LAN interface.

The automated Linux tests use isolated veth interfaces and real client exchanges.
They do not qualify every physical LAN, host-network, or macvlan deployment.

## Lease state and migration

Lease ownership lives in **`<paths.data_dir>/dhcp`**. Preserve the whole directory,
including the initialization marker and SQLite sidecars. Configuration backups
include DHCP settings and reservations, but do not include or reset leases.

For a host move, stop the old service, copy current configuration and the entire
lease-state directory with correct permissions, and keep the old server stopped.
Do not run two servers from cloned ownership state. A stale backup can omit newer
grants; if current ownership cannot be transferred, wait out all possible prior
grants before reusing their addresses. Do not delete initialized state to bypass
recovery errors.

The supervisor pins its data directory at startup. Moving it requires a stopped
service. Trustworthy host time is required. Storage failure suspends DHCP while
preserving ownership; correct the cause, then disable and re-enable DHCP.

## CLI and MCP

The CLI and MCP expose the same operations as the dashboard. Read the saved
revision before mutations, and inspect state after a timeout before retrying.

```sh
dimsum control dhcp
dimsum control dhcp-status
dimsum control dhcp-reservations
dimsum control dhcp-leases --query 'limit=100&state=bound'
dimsum control patch dhcp '{"revision":"READ_SAVED_REVISION","edits":[{"path":["enabled"],"value":false}]}'
dimsum control add dhcp/reservations '{"revision":"READ_SAVED_REVISION","item":{"id":"lab-printer","mac":"02:00:00:00:00:10","address":"192.0.2.20"}}'
dimsum control job '{"kind":"dhcp-check","input":{"probe_other_servers":false}}'
dimsum control jobs
```

MCP tools include `get_dhcp`, `update_dhcp`, `get_dhcp_status`,
`list_dhcp_leases`, and the reservation collection tools. See the
[OpenAPI contract](../api/openapi.yaml) for request schemas.

Lease pages use opaque cursors and return at most 256 rows. Restart paging after
`lease_cursor_expired` or a filter change. Unavailable live inspection does not
mean preserved ownership is empty. There is no force-release operation.
