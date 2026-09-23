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
| Apply a device list | “Add the NSFW list to Jan’s iPhone, leaving other devices unchanged.” |
| Reset an exception | “Reset the tablet's list overrides so it follows its profile again.” |
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
| Devices and local DNS | Client inventory and names, local DNS records; `list_profiles`, `get_client_policy`, `preview_client_policy`, `update_client_policy` |
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

### Edit paths

Collection tools use paths **relative to the collection**, starting with the
zero-based index from the latest read. For example, enabling the second filter
subscription with `update_filter_lists` uses:

```json
{"body":{"revision":"<saved_revision>","edits":[{"path":["1","enabled"],"value":true}]}}
```

Do not include `lists` in that path. Likewise, `update_clients` uses
`["0","name"]`, `update_rules` uses `["0","enabled"]`, and
`update_upstreams` uses `["0"]` to replace the first endpoint. Settings edits
through `update_settings` and `stage_configuration` instead start at the
configuration root, such as `["lists","1","enabled"]`.

A duplicated collection prefix returns `bad_request` with a correction in
`field_errors`. Each field error contains a `path` relative to the request body
and a `message`. Rejected edits do not save or activate configuration.

### Explaining names from query logs

Pass a query's displayed `name` directly to `explain_domain`. Query filtering and
explanation accept the same byte-safe DNS presentation, including labels such as
`r1---edge.example` and three-digit decimal escapes for arbitrary label bytes.
In JSON, escape the backslash: `{"body":{"name":"a\\046b.example"}}` denotes a
label containing a literal dot. An empty name or `.` denotes the DNS root.
Unicode hostnames are also accepted through IDNA normalization, but cannot be
mixed with byte escapes. Configuration hostname validation remains stricter.

Explanation evaluates the active selected policy without making a DNS lookup;
it does not fetch a CNAME chain or reconstruct historical policy. When running
independent diagnostic calls in parallel, collect each success or error rather
than letting one rejected name hide the other results.

### Device policy operations

For **“Add the NSFW list to Jan’s iPhone”**, the agent should:

1. Use `list_clients` to identify the configured `policy_id` and matching evidence,
   and `get_catalog` to find the intended subscription. If names or list choices
   are ambiguous, ask which one; never infer identity from a display name alone.
2. Read `get_client_policy` with `scope: "client"` and that ID. Use its current
   `status.saved_revision`. Promote a legacy naming-only entry to an explicit
   stable ID before or within the same policy mutation.
3. Preview if needed, then call `update_client_policy` once with subscription
   metadata and the device's list assignment together. Use the catalogue's exact
   ID, URL, dialect and domain kind.
4. Read back the device and network policy, activation status and source health.
   Use `explain_domain` with `client_id` to check the active decision. A failed
   first download is not protection; report it even if the assignment was saved.

For a selected HaGeZi NSFW catalogue entry, the MCP arguments are:

```json
{
  "body": {
    "revision": "REV",
    "scope": "client",
    "id": "jan-iphone",
    "subscribe": [{
      "id": "hagezi-nsfw",
      "url": "https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/adblock/nsfw.txt",
      "dialect": "dns-adblock",
      "domain_kind": "suffix",
      "enabled": true
    }],
    "fields": [{"path": ["lists", "hagezi-nsfw"], "value": true}]
  }
}
```

Replace `REV` and the device ID with values from the current read. A newly created
subscription receives `default_apply: false`; an existing subscription retains
its availability and network application. Conflicting metadata or revisions
reject the entire transaction. Configuration remains authoritative YAML, not a
device-policy database. See [inheritance and identity limits](configuration.md#device-policies-and-inheritance).

All device-policy UI operations use the shared API and have CLI/MCP equivalents:

| Operation | CLI after `dimsum control` | MCP |
| --- | --- | --- |
| Inventory / profiles | `clients` / `profiles` | `list_clients` / `list_profiles` |
| Read device policy | `client-policy --query 'scope=client&id=jan-iphone'` | `get_client_policy` with `scope`, `id` |
| Preview a mutation | `client-policy-preview 'JSON'` | `preview_client_policy` with `body` |
| Save a mutation | `patch client-policy 'JSON'` | `update_client_policy` with `body` |
| Explain for a device | `rules-test 'JSON'` | `explain_domain` with `body` |

HTTP uses `GET /api/v1/client-policy`, `POST /api/v1/client-policy/preview`,
`PATCH /api/v1/client-policy`, and `POST /api/v1/rules/test`. CLI/HTTP JSON is the
inner mutation object; only MCP wraps it in `body`. For example:

```sh
dimsum control client-policy --query 'scope=client&id=jan-iphone'
dimsum control patch client-policy '{"revision":"REV","scope":"client","id":"jan-iphone","fields":[{"path":["lists","hagezi-nsfw"],"reset":true}]}'
dimsum control rules-test '{"name":"example.com","client_id":"jan-iphone"}'
```

Field paths are relative to the selected policy: `blocking`, `lists/ID`,
`upstream`, or `rules`. Set with `value`; inherit with `reset: true` rather than
`null`. Use `scope: "profile"` and its ID for profile operations; `scope:
"network"` has no ID. `create: true` creates a client/profile; `delete: true`
deletes an unreferenced owner. `profile: "children"` assigns one profile;
`profile: ""` removes assignment. `reset_all: true` removes policy overrides,
while `reset_pause: true` separately clears a device pause.

Relink with a full `selectors` object or `lease_address`, never both. Selectors
replace the previous set while retaining ID/profile/policy. `lease_address`
requires a current authoritative DHCP lease and saves only its MAC. Explicit
legacy promotion uses `id: "address:192.0.2.25"` and `promote_id: "office-laptop"`.

Device explanation accepts either `client_id` or `address`; address resolution
uses active authoritative selectors even when query observations are unavailable.
Optional `generation` rejects a stale active-policy assumption. Results include
matching method, effective sources and winning rule scope. Explanation does not
query upstreams or inspect an unseen CNAME chain. Query-log rule actions default
to the selected device; profile and network scope must be chosen explicitly.

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
