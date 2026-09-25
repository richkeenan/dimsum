# Device guesses and icons

## Manage rules while dimsum is running

Open **Settings → Device identification rules**. Search by name or domain, add a
custom device family, or edit an installed rule's name, category, explanation,
icon and exact domains. The list distinguishes **Built-in**, **Modified** and
**Custom** entries. Rules can be enabled or disabled. Custom rules can be deleted.

The embedded file is the release baseline. Owner changes are stored separately
under `naming.dns_guesses` in the authoritative configuration:

```yaml
naming:
  dns_guesses:
    rules:
      ring:
        icon: bell
        additions: [another-firmware.example]
        exclusions: [fw-eventstream.ring.com]
    custom:
      - id: example-washer
        name: Smart Washing Machine
        category: appliance
        icon: washing-machine
        domains: [firmware.washer.example, events.washer.example]
```

The example domains are illustrative. Built-in edits store only metadata
overrides, domain additions/exclusions and the optional `disabled` flag. They
do not freeze a copy of the installed rule: future releases can add endpoints
while retaining your changes. Overrides for a baseline removed in an upgrade
are retained and shown as unavailable until that baseline returns or you reset
its customisations. A custom rule keeps its definition if a future release
introduces the same ID.

**Reset** on a modified rule removes its customisations. **Reset all rules**
removes all catalogue customisations, including custom rules, and restores the
defaults in the currently installed version. Manual device names and icons are
separate and are not reset.

Changes activate without a rebuild or restart. History evidence is refreshed
within 30 seconds; evidence from a retired catalogue cannot recreate a removed
guess. Local discovery evidence retains its original validity. At most 256
customisations and 4096 effective domains are supported, with up to 256 domains
per rule.
Names and evidence explanations are single-line text.

### CLI and MCP

To choose an icon, call MCP `list_icons` with optional `search`, for example
`{"search":"plant"}` returns `{"items":["plant-pot"]}`. Search matches a
case-insensitive substring of the installed icon names, ignoring surrounding
whitespace. Omit search to browse the full alphabetical list; no matches returns
an empty list. Use a returned name in `update_client_policy` or
`update_device_rules`.

The same read-only catalogue is available at `GET /api/v1/icons?search=plant`
and through the CLI:

```sh
dimsum icons --query 'search=plant'
```

Use `get_device_rules` and `update_device_rules` over MCP, or the equivalent CLI
commands. Read the saved revision first and read back the catalogue and activation
status after a change:

```sh
dimsum device-rules
dimsum patch device-rules '{"revision":"<saved_revision>","action":"save","id":"example-washer","rule":{"id":"example-washer","name":"Smart Washing Machine","icon":"washing-machine","domains":["firmware.washer.example"]}}'
dimsum device-rules
```

Supported mutation actions:

- `save`: provide `id` and the complete edited `rule`, whose `id` must match.
  Built-in definitions are saved as differences from the installed baseline.
- `enable`: provide `id` and `enabled: true` or `false`.
- `delete`: provide a custom rule's `id`.
- `reset`: provide an `id` to remove that rule's customisations, or omit `id` to
  reset the entire catalogue. Resetting a custom rule removes it.

MCP mutations put these fields in the tool's `body` argument. All operations use
the same revision-checked `/api/v1/device-rules` API. Invalid rules are rejected
atomically, and a revision conflict requires re-reading before retrying.

## Maintaining the shipped defaults

Edit `internal/clients/builtin/dns-guesses.yaml`. The file is embedded in the Go
binary, so changes to this release baseline take effect after rebuilding and
deploying. End users should use the live editor described above.

```yaml
- id: example-washer
  name: Smart Washing Machine
  category: appliance
  icon: washing-machine
  reason: Queries to washing-machine firmware services
  domains:
    - firmware.washer.example
    - events.washer.example
```

These example domains are illustrative. Use device-specific endpoints that
support the identity being suggested, rather than websites, login services or
cloud endpoints shared by multiple product families.

- `id`: unique lowercase identifier containing letters, digits, hyphens or
  underscores. `aws-iot` is reserved for the structured matcher in Go.
- `name`: displayed friendly name.
- `domains`: one or more exact lowercase hostnames, without a trailing dot.
  Wildcards and duplicate domains are rejected.
- `category` (optional): defaults to `unknown`. Choices are `unknown`, `phone`,
  `tablet`, `laptop`, `desktop`, `tv`, `speaker`, `printer`, `camera`, `lighting`,
  `appliance`, `server` and `console`.
- `icon` (optional): a kebab-case [Lucide icon name](https://lucide.dev/icons/),
  such as `washing-machine` or `bell`. Omit it to use the category's default icon.
- `reason` (optional): explanatory text shown under Evidence. If omitted, dimsum
  supplies a generic explanation of DNS-based inference.

Rules require two distinct matching domains or repeated queries to one domain
at least 30 seconds apart. Corroborating evidence expires after 24 hours.
Competing device families suppress the guess; a specific family takes precedence
over generic AWS IoT evidence. Discovery and explicit names take priority over
guessed names. Icons from DNS guesses accompany the inferred classification;
stronger discovered device types keep their own presentation.

The catalogue is validated at startup and in tests. Unknown fields, categories
and icon names are errors. Tests use synthetic YAML definitions to verify the
matching behaviour independently of the shipped device families.

## Manually selecting an icon

In a device's settings, enter a Lucide name in **Icon**. The field offers matching
names and a preview. An explicit icon takes precedence over discovery and DNS
guesses; it does not change the device category or filtering policy. Clear the
field to restore automatic selection.

The authoritative configuration stores it alongside the name:

```yaml
clients:
  - id: utility-room
    name: Utility room washer
    icon: washing-machine
    selectors:
      addresses: [192.0.2.20]
```

Address, CIDR and authoritative DHCP MAC selectors use the same identity
selection as client policy. A MAC-selected icon requires a current,
generation-compatible DHCP lease.

The shared control API and CLI support the same operation. Read the current
saved revision and select the explicit device ID first:

```sh
dimsum clients
dimsum request PATCH /api/v1/client-policy \
  '{"revision":"<saved_revision>","scope":"client","id":"utility-room","icon":"washing-machine"}'
dimsum clients
```

Send `"icon":""` to remove an explicit icon. Omitting `icon` preserves it. For a
legacy address/name entry, either supply `promote_id` when using `client-policy`,
or edit its icon through the `clients` collection's revision-checked PATCH API.
Read back the saved icon and activation status after saving.

## Lucide catalogue maintenance

All icons from the pinned `lucide-react` version are available for manual
selection and built-in guesses. They are loaded on demand from locally embedded
frontend assets; no icon service is contacted.

After upgrading Lucide, run:

```sh
npm --prefix web run icons:generate
```

Commit the generated `internal/clients/builtin/lucide-icons.txt` with the
dependency update. The frontend build checks that backend validation and the
installed frontend catalogue agree. Adding a DNS rule or choosing another
existing icon does not require regenerating this file.
