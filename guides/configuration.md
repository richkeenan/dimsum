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
| `blocking`, `profiles`, `clients` | Network filtering default, reusable policies, stable device identities and overrides. |
| `naming` | Client-name discovery sources. |
| `statistics` | Query history and retention settings. |
| `dhcp` | Optional DHCPv4 settings and reservations. |

Use **Settings**, `dimsum control settings`, or the
[OpenAPI contract](../api/openapi.yaml) for supported fields and edit schemas.
The YAML structures live in `internal/config/config.go`; validation reports
unsupported values and incompatible combinations. The
[native example](../deploy/dimsum.example.yaml) provides installation paths and
wildcard listeners. DHCP has a [separate guide](dhcp.md).

## Discovered client names

When mDNS discovery expires because a device stops responding, dimsum keeps its
last discovered name and device type for up to **48 hours from the discovery
confirmation**. Partial expiry of friendly-name or device metadata also keeps
the richer remembered identity. The name stays visible while discovery refreshes
in the background, without a transient stale-name badge. Advertisement freshness
remains available in device details and the API. The remembered identity takes
priority over DNS-based guesses; newer live discovery replaces it, and explicit
client names and authoritative local/DHCP names retain priority.

Viewing the dashboard or sending ordinary DNS queries does not extend this
window. Discovered names and device evidence are saved in the local
`history.sqlite` database, separately from query-history retention and never in
the configuration. They are restored before requests are served after a restart,
with their original confirmation times and expiry deadlines. Positive hosts-file
and router-PTR results are also restored while their original TTL remains valid.

Names are checkpointed every five seconds and flushed on graceful shutdown;
an abrupt interruption can lose discoveries since the last checkpoint. Storage
failures appear in naming diagnostics while in-memory discovery continues.
The cache holds at most 4096 multicast identities and 4096 resolved names.
Changes to discovery-source settings invalidate remembered names; unrelated
policy or configured-name edits do not. Use an explicit client name for a lasting
label.

**Last seen** records the client's latest DNS query in the selected history
range, not its latest name advertisement. A printer can therefore keep a fresh
discovered name even when its last DNS query was yesterday.

## Applying changes

Read `saved_revision` before a mutation and send it with the requested edits.
If another writer changes the document, reload and review the current values
before retrying. Saving configuration and activating it are distinct operations:
inspect the active generation, pending state, and errors after saving.

Listener, secure-cookie, and runtime path changes can require a restart. Ordinary
rules, client names, and supported settings apply through the coordinator.
Preserve the configuration directory's ownership and permissions. Avoid mounting
only the file, since atomic saves replace its directory entry.

## Device policies and inheritance

Policy settings follow **network defaults → one optional profile → device
overrides**. Profiles cannot inherit other profiles. Omitted settings inherit;
explicit `true` or `false` remains an override even when equal to its parent.
Reset removes the override, so later parent changes apply again. Resetting all
device overrides retains its stable ID, selectors and profile assignment; reset
the pause separately. Removing a profile assignment resumes network inheritance.

For example, merge these sections into your configuration. The list URL and
upstream below are documentation placeholders, not working services:

```yaml
blocking: true
lists:
  - id: adult
    url: https://example.test/adult.txt
    dialect: domains
    domain_kind: suffix
    enabled: true
    default_apply: false
profiles:
  - id: children
    name: Children
    policy:
      lists: {adult: true}
clients:
  - id: jan-iphone
    name: Jan's iPhone
    selectors:
      macs: ['02:00:00:00:00:01']
    profile: children
    overrides:
      upstream:
        upstreams: ['192.0.2.53:53']
        fallback_upstreams: ['192.0.2.54:53']
  - id: tablet
    name: Tablet
    selectors:
      addresses: [192.0.2.20, '2001:db8::20']
    profile: children
    overrides:
      lists: {adult: false}
```

Jan's phone inherits the profile's list and uses an explicit upstream route. The
tablet excludes that list even if the profile or network default later changes.
Deleting the tablet's `overrides.lists.adult` key makes it follow the profile.
Primary and fallback upstream arrays form one setting and are replaced together.
Custom owner rules use the same `id`, `kind`, `action`, `pattern`, and `enabled`
fields as network `rules`, under profile `policy.rules` or device `overrides.rules`.

### Subscription versus application

`enabled` controls whether a subscription is available for downloading/loading.
`default_apply` controls its network application. Omitting `default_apply`
preserves legacy behavior: enabled subscriptions apply network-wide. Profiles
and devices choose **inherit / on / off** for each subscription. An assignment
does not enable a disabled subscription.

Adding a new catalogue list from a device can subscribe and apply it in one
transaction with `default_apply: false`, leaving peers unaffected. Applying an
existing globally applied list does not remove it from peers. A list cannot be
deleted while any profile/device references it, including explicit Off entries;
a profile cannot be deleted while assigned to a device.

Check both effective assignment and source health. A failed first download has
no usable membership even if assignment is On. A later refresh failure can retain
previously downloaded usable rules. Saved policy, active policy and download
health are shown separately in Clients and Filter lists.

### Built-in work compatibility list

The **Work tools compatibility** catalogue choice is an independent, best-effort
allowlist for common analytics, marketing, attribution, experimentation, and
monitoring services. It is embedded in the executable, works offline, and updates
with dimsum rather than downloading from a list publisher. Its source and vendor
references live in `internal/lists/builtin/work-compatibility.adblock`.

Apply it to a Work profile alongside your existing blocking lists, with filtering
enabled. It permits the listed domains and their subdomains, including tracking
by those services. Other domains continue to follow the profile's blocking lists.
Custom domains and self-hosted deployments may need additional exceptions.

For example, merge these entries into the existing `lists` and `profiles`
collections; keep your existing blocklist subscriptions:

```yaml
lists:
  - id: work-compatibility
    url: builtin://work-compatibility
    dialect: dns-adblock
    domain_kind: suffix
    enabled: true
    default_apply: false
profiles:
  - id: work
    name: Work
    policy:
      blocking: true
      lists: {work-compatibility: true}
```

The shared client-policy API, CLI, and MCP accept this built-in URL just like a
subscription URL. No hosting or external download is required.

You can edit the exceptions shared by every network, profile, or device that
selects a built-in subscription. The installed baseline stays in the executable;
only your changes are saved in authoritative YAML:

```yaml
    builtin_overrides:
      additions: [metrics.example.com]
      exclusions: [segment.io]
```

Add this field to the subscription, alongside `default_apply`. Domains must be
canonical lowercase names without a trailing dot in YAML. Each exception allows
the domain and its subdomains. Excluding an entry removes that exception and
restores normal policy matching; it does not create a deny rule. A broader allow
exception or another selected allowlist can still allow the domain.

Read `GET /api/v1/builtin-lists/{id}`, using the configured subscription ID rather
than the built-in URL. It returns `id`, `revision`, `status`, `entries`, and
`customized`. Each entry contains `domain`, `origin` (`builtin` or `custom`), and
`removed`. Entries are sorted by domain. This endpoint does not edit remote lists.

Send `PATCH` to the same URL with the current `revision`, an `action`, and a
`domain` for single-domain operations:

- `add`: add a suffix exception, or re-enable an excluded baseline entry.
- `remove`: exclude a baseline exception, or delete a custom addition.
- `restore`: clear a baseline exclusion.
- `reset`: omit `domain`; clear all additions and exclusions to use the installed
  baseline.

The API normalizes case and a trailing root dot. Saves return the usual activation
status; stale revisions fail with HTTP 409. Inspect the returned status or read
back before assuming activation. CLI equivalents are:

```sh
dimsum control builtin-list work-compatibility
dimsum control patch builtin-lists/work-compatibility \
  '{"revision":"REVISION_FROM_GET","action":"add","domain":"metrics.example.com"}'
dimsum control patch builtin-lists/work-compatibility \
  '{"revision":"CURRENT_REVISION","action":"reset"}'
```

MCP exposes these operations as `get_builtin_list` and `update_builtin_list`.
Customizations survive restart, recovery, backup/restore, and binary updates.
Recovery reapplies them to the installed baseline. Exclusions remain saved even
when a release no longer ships that domain, so a later release cannot silently
restore it. Reset always uses the baseline in the installed executable.

### Identity and portability

The stable `id`, selectors, profile and overrides live in YAML and survive
configuration backup/restore. Matching uses **explicit address → authoritative
DHCP MAC → longest matching CIDR → network default**. Selectors accept multiple
`addresses`, `macs` or `cidrs`; ambiguous duplicate selectors are rejected.
Existing `{address, name}` entries retain their legacy behavior.

Ordinary DNS packets do not contain a MAC. MAC matching requires an unexpired,
committed lease from dimsum's authoritative DHCP service; discovery names,
reservations alone and display labels do not establish identity. Private Wi-Fi
MACs may change between networks. A restored ID preserves policy, not automatic
recognition of hardware on a new network.

Use **Relink** to replace selectors while preserving ID and policy. Creating or
relinking from an authoritative lease saves its MAC only, without retaining a
dynamic IP that might later belong to a peer. Address selectors suit stable
addresses; reassess them when moving configuration. The inventory shows the
actual matching method. Names are labels and need not be unique.

### Rules, pauses and caches

Custom rule precedence is **device → profile → network**. Within each scope,
allow/deny, specificity and stable rule-ID precedence remain unchanged. Applicable
subscription allows precede subscription denies. Resetting an owner's rules
removes that owner's collection and exposes broader rules again. Original-name
and response-alias checks use the same captured device policy.

Global pause dominates every device setting. Device `paused_until` is an absolute
RFC3339 timestamp; expiry resumes its effective policy. Effective `blocking:
false` also suppresses ordinary filtering. Local DNS handling and private-reverse
protection keep their precedence, including during pauses.

Devices on the same complete upstream route share raw answer caches and in-flight
requests; answers are filtered separately for each device. Different routes
isolate caches, coalescing and stale refreshes. Policy publication changes the
generation used for cache lookup, and in-flight requests retain their captured
policy. Subscription indexes are shared, not copied once per device.

See [agent control](agent-control.md#device-policy-operations) for CLI, HTTP and
MCP examples, including scoped resets and explanations.
