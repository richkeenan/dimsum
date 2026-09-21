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
- Manage the same server through the browser, JSON CLI or an LLM agent with shell access.

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

For a source build, use **Go 1.26.8** and **Node.js 24.21.0 with npm**. Build the
web assets first, then the executable for your machine:

```sh
sh scripts/build-web.sh
mkdir -p dist
go build -trimpath -o dist/dimsum ./cmd/dimsum
./dist/dimsum version
```

Linux amd64 and arm64 are the deployment targets. You can also build and run
locally on macOS for development. The executable embeds the assets produced by
the web build; rebuild it after changing the frontend for the embedded UI to update.

For cross-built Linux binaries, run `sh scripts/build-local.sh` (also requires
Python 3). For tarballs, Debian packages and container builds, see the
[installation guide](docs/operations/install.md).

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

### 2. Set your admin password

There is **no default admin password or username**. Bootstrap a password before
your first login. Choose one between 12 and 1,024 bytes long.

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

Open **http://127.0.0.1:18080/** and enter the password you just created.
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

Use a 64-bit Linux installation. Native systemd installation is the primary
deployment path; the repository also includes Linux Docker Compose support.

- **Native service:** follow the [systemd installation steps](docs/operations/install.md#native-systemd-installation)
  to create the service user, install the executable and unit, bootstrap the
  password, and start the service.
- **Debian package:** build a local `.deb` using the
  [packaging instructions](docs/operations/install.md#standalone-and-native-service-packages).
  Installing the package does not create your configuration or start the service.
- **Docker Compose:** follow the [container setup](docs/operations/install.md#dockercompose-on-linux)
  for configuration/state mounts, directory ownership and password bootstrap.

For devices on your network, configure `dns.listen` with the server's LAN address
and port `53`, then set that address as their DNS server, usually through your
router's DHCP settings. Both UDP and TCP port 53 must be available. The supplied
systemd unit grants the nonroot service permission to bind that port.

For remote web access to a loopback-only admin listener, use an SSH tunnel:

```sh
ssh -N -L 18080:127.0.0.1:18080 user@dns-server
```

Then open **http://127.0.0.1:18080/** on your laptop. This assumes the remote admin
listener uses port 18080, as in the quick start. The packaged example uses 8080;
for that configuration, use `-L 8080:127.0.0.1:8080` and open port 8080 instead.

See [Pi-hole migration](docs/operations/migration.md) if you want to bring across
an existing setup.

## Command-line and LLM/agent control

An LLM agent can control dimsum with ordinary shell commands, locally or over SSH.
Give it access to the CLI as the operating-system user that runs dimsum. The CLI
uses an owner-only Unix socket and returns JSON; it does not require the browser
password. dimsum does not require an LLM API key or an MCP server.

For the local quick start, copy the `control` path from the server's startup JSON:

```sh
export DIMSUM_CONTROL_SOCKET='/actual/path/from/startup/control'
./dist/dimsum control help
./dist/dimsum control diagnostics
./dist/dimsum control settings
./dist/dimsum control lists
./dist/dimsum control queries --query 'limit=25'
```

For the native service, the default socket is `/run/dimsum/control.sock`:

```sh
ssh user@dns-server 'sudo -u dimsum /usr/bin/dimsum control help'
ssh user@dns-server 'sudo -u dimsum /usr/bin/dimsum control diagnostics'
```

The SSH user needs permission to run those commands as `dimsum`. For unattended
agents, arrange that access through your existing SSH/sudo setup.

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

Example prompt for your agent:

> Manage my dimsum server over SSH at `user@dns-server`. Run
> `sudo -u dimsum /usr/bin/dimsum control help` to discover the CLI. Inspect the
> current settings and recent queries, then add an exact deny rule for
> `ads.example.org`. Use the current saved revision when making the edit, verify
> it with the rule tester, and summarize the resulting configuration change.

See the [control reference](docs/operations/agent-control.md) for grouped edits,
job handling, API access and structured response details.

## Development

### Frontend with live reload

The Vite dev server runs the frontend only. You must also run the Go backend and
bootstrap its admin password using the steps above.

For the quick-start backend on port 18080:

1. Change **both** `/api` and `/session` proxy targets in
   `web/vite.config.ts` from `http://127.0.0.1:8080` to `http://127.0.0.1:18080`.
2. Add `allowed_hosts: ["127.0.0.1:5173"]` under `admin` in your YAML config.
3. Restart the Go server after changing `allowed_hosts`.
4. Start Vite from another terminal:

```sh
npm --prefix web run dev -- --port 5173 --strictPort
```

Open **http://127.0.0.1:5173/** and use the same admin password. If you use Bun,
`bun run dev` from `web/` also starts only Vite; it does not start dimsum or create
a password. Use the port Vite prints and match it in `admin.allowed_hosts`.

If you see an error from another application, check the proxy targets: another
service may own port 8080. A connection-refused error means the configured backend
is not listening. For the embedded UI at port 18080, Vite's proxy settings do not
apply.

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
