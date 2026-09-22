# Control dimsum with your AI agent

You can run day-to-day DNS administration from a conversation with your agent:
investigate a blocked domain, change filtering, name devices, manage local DNS,
configure upstreams, and back up or restore configuration. Connect through
dimsum's built-in Model Context Protocol (MCP) server to give your agent access
to the running service.

Your agent reads and changes the same configuration you see in the dashboard
and CLI. Through the shared control API, it can preserve YAML comments, check
for conflicting edits, and verify whether a saved change has taken effect.

## Connect your agent

Start with a [running dimsum installation](deployment.md) and an agent client
that supports Streamable HTTP MCP with a custom authorization header. The
agent client must be able to reach your dimsum server.

1. Sign in to the dimsum dashboard and open **Settings → Agent access**.
2. Enter a token name, such as `Desktop assistant`, and choose **Create token**.
3. Copy the token or use **Copy connection fields** before closing. You cannot
   view the token again.
4. Add an MCP server in your agent client with these connection fields:

   | Field | Value |
   | --- | --- |
   | Name | `dimsum` |
   | Transport | HTTP / Streamable HTTP |
   | URL | The MCP URL shown in Settings, for example `http://dns-server:8080/mcp` |
   | Header name | `Authorization` |
   | Header value | `Bearer YOUR_TOKEN` |

5. Connect and ask: “Use dimsum to check whether blocking is enabled and report
   any pending configuration changes.”

Use the URL from your own dashboard and replace `YOUR_TOKEN` with the copied
token. Client configuration formats vary; enter these values in your client's
MCP settings.

Tokens grant full administrator access until you revoke them in **Settings →
Agent access**. Give each client a named token so you can revoke its access
independently. Keep tokens in your client's credential storage. Use HTTPS when
credentials cross an untrusted network; see the
[HTTPS deployment instructions](deployment.md#https-and-access-controls).

## Ask your agent to manage DNS

Use these requests as starting points. The addresses and domains below are
documentation examples; substitute your own when making changes.

| Task | Example request |
| --- | --- |
| Inspect traffic | “Which devices made the most DNS requests in the last hour, and which domains did dimsum block most?” |
| Investigate filtering | “Why is ads.example.com blocked? Show me the matching rule or filter list.” |
| Change a rule | “Allow updates.example.com and verify the active policy allows it.” |
| Pause blocking | “Pause blocking for five minutes and tell me when it will resume.” |
| Resume blocking | “Turn blocking back on and verify its state.” |
| Manage subscriptions | “Show me the available filter-list subscriptions and which ones I have enabled.” |
| Refresh lists | “Refresh my filter lists and report whether the job succeeded.” |
| Name a device | “Rename the device at 192.0.2.25 to Office laptop.” |
| Add local DNS | “Create a local A record for printer.home.arpa pointing to 192.0.2.30.” |
| Configure upstreams | “Show my upstream DNS servers and switch my primary upstream to the Quad9 DNS-over-HTTPS preset.” |
| Check performance | “Show DNS response-time percentiles for the last hour and check upstream diagnostics.” |
| Back up configuration | “Create a configuration backup and check that the job completed.” |
| Manage DHCP | “Show my DHCP leases and reservations, and check whether the saved DHCP settings have taken effect.” |

You can combine investigation and a change in one request:

> “Check recent blocked queries from 192.0.2.25 for updates.example.com. Explain
> the current blocking decision, add an allow rule for that domain, then verify
> the saved rule and active policy.”

The agent can inspect retained query history and explain the current policy
decision. The domain-explanation tool evaluates filtering rules without making
a DNS lookup. History depends on your retention settings and may not cover the
whole period you request.

## Controls available to your agent

dimsum advertises structured MCP tools with input schemas. Your client discovers
them when it connects, so you can ask for a task without knowing tool names.

| Area | Tools and operations |
| --- | --- |
| Traffic and performance | `get_summary`, `get_rankings`, `get_timeseries`, `get_performance`, `list_queries`, `get_query` |
| Filtering | `get_blocking`, `set_blocking`, `explain_domain`; list, add, edit, and remove rules and filter subscriptions; inspect the subscription catalog |
| Devices and local DNS | List, add, edit, and remove client-name overrides and local DNS records |
| Upstreams and settings | List, add, edit, and remove upstreams; `get_settings`, `update_settings`, `get_diagnostics` |
| Staged configuration | `stage_configuration` to validate edits, then `commit_configuration` to commit them |
| Maintenance | `create_job` for refresh, backup, restore, and diagnostics; `list_jobs` to inspect results |
| DHCPv4 | Inspect and edit settings, check runtime status, list leases, and manage reservations |

See the [configuration guide](configuration.md),
[encrypted upstream guide](encrypted-upstreams.md), and [DHCP guide](dhcp.md)
for details. The [OpenAPI contract](../api/openapi.yaml) defines the operations;
entries under `x-mcp-tools` identify the MCP tools.

For host installation, binary upgrades, and service restarts, an agent needs
host access through your deployment tools or shell. Token management, password
changes, and backup-file downloads use the dashboard, CLI, or HTTP API rather
than MCP tools. You can start backup and restore jobs through MCP.

## How your agent should apply changes

dimsum supplies operating instructions to connected agents. For configuration
changes, your agent should:

1. Read the current configuration and `saved_revision` using `get_settings` or
   the relevant collection tool.
2. Submit the requested change with that value as `revision`. If another client
   has edited the configuration, reread it before retrying.
3. Read back the saved values and activation status. Report any pending changes,
   activation errors, or restart requirements rather than treating a successful
   save as proof that the change is active.
4. For background jobs, check `list_jobs` for completion and the result. For
   DHCP changes, check `get_dhcp_status` for the applied state.

If you keep instructions for your agent, you can include:

> Use dimsum MCP tools for DNS administration. Before editing configuration,
> read the current saved revision. After editing, read back the saved values
> and activation status. Check background jobs for completion. Report what
> changed and whether it is active.

## Connection troubleshooting

- **Cannot connect:** check that the agent client can reach the MCP URL from
  where it runs. A cloud-hosted client may not have access to your private LAN.
- **Authentication fails:** check the `Authorization` header includes `Bearer `
  before the token. If you lost or revoked the token, create a new one in Settings.
- **Tools or result schemas look out of date after an upgrade:** reconnect the
  MCP server in your client to refresh tool definitions.
- **A change times out:** ask the agent to inspect the current configuration or
  job result before retrying. The server may have completed the operation.
- **The save succeeded but behavior has not changed:** inspect activation status.
  Listener, secure-cookie, and runtime path changes can require a service restart.
