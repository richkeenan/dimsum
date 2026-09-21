<p align="center">
  <img src="web/src/assets/dimsum.svg" width="200" alt="dimsum’s happy lotus-leaf rice parcel mascot">
</p>

# dimsum

dimsum is a DNS ad blocker for your home or private network. Run it on a Linux
server or Raspberry Pi, point your devices at it for DNS, and manage filtering
through a web dashboard or command-line interface.

It forwards allowed queries to your chosen upstream DNS servers and blocks names
that match your rules and subscriptions. A single Go executable includes the web
application; you don't need Node.js to run it.

## What you can do

- Block ads and trackers with subscriptions, custom deny rules and allow rules.
- Configure local DNS records and zones.
- View query history, client activity, blocking statistics and upstream health.
- Use a bounded DNS cache with optional stale answers and background refresh.
- Back up and restore configuration and credentials, or import a Pi-hole setup.
- Manage the same server through the browser, JSON API/CLI or an HTTP MCP agent.

Configuration lives in a comment-preserving YAML file. The UI and CLI update that
file through the running server. Statistics, cached lists and recovery state live
separately.

**Project status:** the implementation includes the resolver, cache, statistics,
web UI, CLI and packaging. Public release publishing is disabled; build from
source for now. Raspberry Pi qualification and a household trial remain
outstanding. See [implementation status](docs/implementation-status.md) for the
current evidence and known limits.

## Download and build

Clone the repository:

```sh
git clone https://github.com/richkeenan/dimsum.git
cd dimsum
```

For source builds, install **Go 1.26.8**, **Node.js 24.21.0 with npm**,
**GoReleaser 2.18.2**, and **Python 3** (dependency/provenance metadata).
GoReleaser is the single build entry point; `.goreleaser.yaml` owns the build
flags and version information. For an executable for your current machine:

```sh
goreleaser build --snapshot --clean --single-target --output dist/dimsum
./dist/dimsum version
```

Linux amd64 and arm64 are the deployment targets. You can also build and run
locally on macOS for development. The frontend is a TanStack Start static SPA.
`npm --prefix web run build` produces `web/dist/client/` and copies it into
`internal/webassets/dist/` through `web/scripts/embed.mjs`. Rebuild the Go
executable after frontend changes to update its embedded UI; no Node runtime is needed.

For Linux amd64/arm64 binaries, tarballs, Debian packages, and checksums:

```sh
sh scripts/build-local.sh
# Runs: goreleaser release --snapshot --clean --skip=publish,docker
```

Artifacts are written to `dist/`. Both commands run the frontend build through
GoReleaser's hooks. See the [installation guide](docs/operations/install.md)
for container builds and build provenance.

## Quick start: run locally

Run the following commands from the repository root. This example uses DNS port
**15353** and web port **18080**, so you can try dimsum alongside another DNS
service. It does not change your machine's DNS settings.

### 1. Create a configuration

```sh
mkdir -p artifacts/dev
chmod 700 artifacts/dev
cat > artifacts/dev/dimsum.yaml <<'YAML'
version: 1
dns:
  listen: ["127.0.0.1:15353"]
  upstreams: ["1.1.1.1:53", "9.9.9.9:53"]
admin:
  listen: "127.0.0.1:18080"
paths:
  data_dir: data
  secrets_dir: secrets
rules:
  - id: example-block
    action: deny
    kind: exact
    pattern: ads.example.test
    enabled: true
YAML
./dist/dimsum validate -config artifacts/dev/dimsum.yaml
```

Data and secret paths are relative to the configuration file. This setup stores
them under `artifacts/dev/`, which Git ignores. It starts with one example rule;
choose subscriptions in **Filter lists** after signing in.

### 2. Choose an admin password (optional)

Fresh installs automatically use the password **`admin`**, with no username.
Existing credentials are preserved. You can change the password in **Settings**
after signing in; there is no forced-change step. Passwords accept 1–1,024 UTF-8 bytes.

To choose a different password before the first start, optionally bootstrap it:

This command uses Python 3 to prompt without echoing your password and passes it
to dimsum over standard input:

```sh
python3 -c 'import getpass; print(getpass.getpass("New dimsum admin password: "))' \
  | ./dist/dimsum bootstrap -config artifacts/dev/dimsum.yaml -password-file -
```

Successful setup prints `{"created":true}`. dimsum stores a salted password hash
in the configured secrets directory. Bootstrap refuses to overwrite existing
credentials; it is a first-time setup command, not a password-reset command.

For automation, use `-password-file /path/to/password-file` with an owner-only
regular file (mode `0600`). The secrets directory must be owner-only (`0700`).
See [recovery](docs/operations/recovery.md) for existing installations and restores.

### 3. Start the server and open the dashboard

```sh
./dist/dimsum serve -config artifacts/dev/dimsum.yaml
```

Open **http://127.0.0.1:18080/** and sign in with **`admin`**, or your existing or
bootstrapped password.
The Go server serves both the dashboard and its API. No frontend dev server is
needed for this view. Stop the server with **Ctrl+C**.

Startup prints JSON containing the DNS, admin and local control-socket addresses.
Query history starts empty until dimsum receives DNS traffic.

### 4. Try DNS queries

In another terminal, with `dig` installed:

```sh
dig @127.0.0.1 -p 15353 example.com
dig @127.0.0.1 -p 15353 ads.example.test
```

The first query forwards upstream; the second exercises the example block rule.
Open **Query log** to inspect the results.

## Install on a server or Raspberry Pi

Use a 64-bit Linux installation with systemd. Build the artifacts above and copy
the matching archive and `dist/checksums.txt` to the server. Raspberry Pi OS
64-bit uses `linux_arm64`; an Intel/AMD server uses `linux_amd64`.

### Fresh systemd installation

Run these commands on the server, in the directory containing the archive:

```sh
sha256sum --check --ignore-missing checksums.txt
mkdir -p /tmp/dimsum-install
tar -xzf dimsum_*_linux_arm64.tar.gz -C /tmp/dimsum-install
cd /tmp/dimsum-install
sudo install -m 0755 dimsum /usr/bin/dimsum
sudo install -m 0644 deploy/dimsum.sysusers /usr/lib/sysusers.d/dimsum.conf
sudo systemd-sysusers /usr/lib/sysusers.d/dimsum.conf
sudo install -d -o dimsum -g dimsum -m 0750 /etc/dimsum /var/lib/dimsum
sudo install -d -o dimsum -g dimsum -m 0700 /etc/dimsum/secrets
sudo install -o dimsum -g dimsum -m 0600 deploy/dimsum.example.yaml /etc/dimsum/dimsum.yaml
sudo install -m 0644 deploy/dimsum.service /usr/lib/systemd/system/dimsum.service
sudoedit /etc/dimsum/dimsum.yaml
sudo -u dimsum /usr/bin/dimsum validate -config /etc/dimsum/dimsum.yaml
sudo systemctl daemon-reload
sudo systemctl enable --now dimsum
```

The example initially listens on loopback DNS port **5353** and admin port
**8080**. Set the intended listeners before starting it. These configuration
creation commands are for a fresh installation; upgrades use the existing files.

`enable --now` starts dimsum immediately **and at every boot**. The service runs
as a dedicated nonroot user, waits for network-online startup, and restarts after
a crash with a two-second delay. Logs go to the system journal:

```sh
systemctl is-enabled dimsum
systemctl status dimsum
journalctl -u dimsum -f
sudo systemctl restart dimsum
```

To verify boot startup, reboot the server, reconnect, and check `systemctl
is-active dimsum` plus a DNS lookup against its configured address. Explicitly
stopping the service with `systemctl stop dimsum` leaves it stopped until you
start it again or reboot.

### Upgrading a Pi

From a clean, committed checkout on your build machine:

```sh
sh scripts/deploy-pi.sh pi@dns-server http://dns-server:18080/health/ready
```

Use the actual health URL reachable **from the Pi**, including its configured
admin port. The script builds with GoReleaser, transfers the arm64 archive,
verifies its checksum, replaces `/usr/bin/dimsum`, restarts `dimsum.service`,
and waits for readiness. It retains `/usr/bin/dimsum.previous` and restores it
if startup or the health check fails. Configuration, credentials, and history
stay in place. SSH access and passwordless sudo for the upgrade are required.

The script upgrades an existing permanent service; it does not perform the
first installation or switch the network from another resolver.

### Debian package installation

On a fresh server, the GoReleaser `.deb` installs the binary, service unit, and
example configuration. Create the service account and configuration, then enable
the service explicitly:

```sh
sudo apt install ./dimsum_*_arm64.deb
sudo systemd-sysusers /usr/lib/sysusers.d/dimsum.conf
sudo install -d -o dimsum -g dimsum -m 0750 /etc/dimsum /var/lib/dimsum
sudo install -d -o dimsum -g dimsum -m 0700 /etc/dimsum/secrets
sudo install -o dimsum -g dimsum -m 0600 /usr/share/doc/dimsum/dimsum.example.yaml /etc/dimsum/dimsum.yaml
sudoedit /etc/dimsum/dimsum.yaml
sudo -u dimsum /usr/bin/dimsum validate -config /etc/dimsum/dimsum.yaml
sudo systemctl daemon-reload
sudo systemctl enable --now dimsum
```

Use the `amd64.deb` package for Intel/AMD servers. [Docker Compose](docs/operations/install.md#dockercompose-on-linux)
is also available.

For devices on your network, configure `dns.listen` with the server's LAN address
and port `53`, then set that address as their DNS server, usually through your
router's DHCP settings. Both UDP and TCP port 53 must be available. The supplied
systemd unit grants the nonroot service permission to bind that port.

When deliberately replacing Pi-hole, disable its boot startup as part of the
cutover (`sudo systemctl disable --now pihole-FTL`). Otherwise both services may
try to bind port 53 after a reboot. Keep its installation and configuration for
rollback. See the [migration guide](docs/operations/migration.md).

For remote web access to a loopback-only admin listener, use an SSH tunnel:

```sh
ssh -N -L 18080:127.0.0.1:18080 user@dns-server
```

Then open **http://127.0.0.1:18080/** on your laptop. This assumes the remote admin
listener uses port 18080, as in the quick start. The packaged example uses 8080;
for that configuration, use `-L 8080:127.0.0.1:8080` and open port 8080 instead.

See [Pi-hole migration](docs/operations/migration.md) if you want to bring across
an existing setup.

## Connect your agent

1. Open **Settings → Agent access** and create a named token.
2. Add an **HTTP / Streamable HTTP MCP server** in your agent client using the
   displayed URL, such as `http://dns-server:18080/mcp`.
3. Set the connection header to `Authorization: Bearer YOUR_TOKEN`.

The agent discovers typed tools for traffic, devices, query details, blocking
explanations, settings and configuration changes. Ask “Which devices made the
most DNS requests in the last hour?” or “Why is this domain blocked?” No SSH,
shell setup, model API key or separate MCP process is required. Your agent client
must be able to reach the server's network address and support custom auth headers.

Tokens grant administrator access, are shown only when created, and remain valid
until revoked in Settings. They also authenticate the regular HTTP API. The
OpenAPI 3.1 contract is available at `/api/v1/openapi.json` and
`/api/v1/openapi.yaml` using a token or signed-in browser session.

## Command-line control

The CLI remains available for local administration and automation. It uses an
owner-only Unix socket and returns JSON without a browser password.

For the local quick start, copy the `control` path from the server's startup JSON:

```sh
export DIMSUM_CONTROL_SOCKET='/actual/path/from/startup/control'
./dist/dimsum control help
./dist/dimsum control diagnostics
./dist/dimsum control settings
./dist/dimsum control lists
./dist/dimsum control catalog
./dist/dimsum control queries --query 'limit=25'
```

For the native service, the default socket is `/run/dimsum/control.sock`.

### Make a configuration change

Read `control settings` first. Copy `status.saved_revision` into a mutation so
the server can detect edits made by another user or agent:

```sh
./dist/dimsum control add rules '{"revision":"REVISION_FROM_SETTINGS","item":{"id":"agent-example","kind":"exact","action":"deny","pattern":"ads.example.org","enabled":true}}'
./dist/dimsum control rules-test '{"name":"ads.example.org"}'
```

Use `control help` to discover list management, records, clients, blocking controls,
backup/restore jobs and diagnostics. Commands accept JSON inline, from `@FILE`,
or from standard input with `@-`. Exit code **5** means a revision conflict: read
the current settings and reconsider the change before retrying.

See the [control reference](docs/operations/agent-control.md) for grouped edits,
job handling, API access and structured response details.

### Change the admin password

With the server running and the control socket selected:

```sh
python3 -c 'import getpass, json; print(json.dumps({"password": getpass.getpass("New dimsum admin password: ")}))' \
  | ./dist/dimsum control password @-
```

This is also available in **Settings** and through `POST /api/v1/password` with
`{"password":"..."}`. A successful change revokes all browser sessions; sign in again.

## Development

### Frontend with live reload

The TanStack Start/Vite dev server runs the frontend only. Run the Go backend
using the quick-start steps above.

For the quick-start backend on port 18080:

1. Add `allowed_hosts: ["127.0.0.1:5173"]` under `admin` in your YAML config.
2. Restart the Go server after changing `allowed_hosts`.
3. Start the frontend from another terminal:

```sh
npm --prefix web run dev -- --port 5173 --strictPort
```

Open **http://127.0.0.1:5173/** and use the same admin password. Both `/api` and
`/session` proxy to `http://127.0.0.1:18080` by default. To use another backend:

```sh
DIMSUM_API_URL=http://127.0.0.1:8080 npm --prefix web run dev -- --port 5173 --strictPort
```

Match the frontend host and port in `admin.allowed_hosts`. A connection-refused
proxy error means the backend is not listening at `DIMSUM_API_URL`.

### Tests and API types

```sh
go test ./...
go test -race ./...
go vet ./...
npm --prefix web run typecheck
npm --prefix web test
npm --prefix web exec -- playwright install chromium
npm --prefix web run test:e2e
```

Regenerate frontend types after editing `api/openapi.yaml`:

```sh
npm --prefix web run api:generate
```

See [AGENTS.md](AGENTS.md) for repository conventions and
[acceptance testing](docs/operations/acceptance.md) for opt-in integration and
deployment checks.

## Further reading

- [Configuration, filtering and local DNS](docs/05-filtering-and-configuration.md)
- [Install and package](docs/operations/install.md)
- [Backups, recovery and rollback](docs/operations/recovery.md)
- [Pi-hole migration](docs/operations/migration.md)
- [CLI and agent control](docs/operations/agent-control.md)
- [Architecture](docs/01-product-and-architecture.md)
- [Implementation status and measured limits](docs/implementation-status.md)

The project license has not yet been selected. Dependency notices and packaging
details are documented in the installation guide.
