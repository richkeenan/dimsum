# dimsum on macOS

Docker Desktop provides the simplest Mac setup for serving DNS. A native
foreground executable is also available for local use and development.

## Docker Desktop quickstart

Install [Docker Desktop for Mac](https://docs.docker.com/desktop/setup/install/mac-install/)
for Apple silicon or Intel and start it. These commands use its Docker Compose
plugin; `docker compose version` should succeed.

The release workflow is configured to publish the Compose asset and multi-platform
GHCR images on a future tagged release. They are not published by pushing these
changes alone. Use the following quickstart once that release is available:

```sh
mkdir -p ~/dimsum-docker
cd ~/dimsum-docker
curl -fL https://github.com/richkeenan/dimsum/releases/latest/download/compose.desktop.yaml -o compose.yaml
docker compose up -d --wait
```

Run subsequent Compose commands from this directory. Open
<http://localhost:8080> and sign in with the initial password **`admin`**. Change
it in **Settings**, then choose subscriptions in **Filter lists**. Existing
credentials are preserved on restart and upgrade.

The dashboard and MCP are published only on loopback by default. Create a token
in **Settings → Agent access** and connect your local agent to
`http://localhost:8080/mcp` with its bearer token. See [agent control](agent-control.md).

The image is `ghcr.io/richkeenan/dimsum:latest`, with Linux arm64 and amd64 variants
selected automatically. To pin a release, put `DIMSUM_IMAGE=ghcr.io/richkeenan/dimsum:VERSION`
in a `.env` file beside `compose.yaml`, replacing `VERSION` with the release
version **without its leading `v`**. `latest` follows stable releases. After the
first image publication, the package owner may need to change GHCR package
visibility to **Public** to allow pulls without authentication.

### Storage and initial defaults

Compose creates named volumes `dimsum_config` and `dimsum_state`:

| Volume | Container path | Contents |
| --- | --- | --- |
| `config` | `/etc/dimsum` | `dimsum.yaml` and restricted secrets |
| `state` | `/var/lib/dimsum` | Statistics, downloaded lists, other data, and recovery state |

New empty volumes inherit default configuration and ownership from the image.
The service runs as UID/GID 65532. Startup creates the initial password only when
no credential exists. Image upgrades preserve populated volumes rather than
replacing your settings with new defaults. The control socket in `/run/dimsum`
is temporary and is recreated at startup.

### DNS, port conflicts, and LAN access

By default, TCP and UDP DNS are published on **port 53 on all IPv4 host interfaces**.
Give the Mac a stable LAN address and configure your router or devices to use
that address for DNS. Allow the intended LAN through the Mac's firewall and
keep DNS restricted to your private network. The dashboard remains local.

If a port is already occupied, set overrides in `.env` beside `compose.yaml`:

```dotenv
DIMSUM_DNS_BIND=0.0.0.0
DIMSUM_DNS_PORT=1053
DIMSUM_ADMIN_PORT=18080
```

Run `docker compose up -d --wait` again. Then admit the dashboard's new host port
through the local control socket. Read `saved_revision` from the first command
and substitute it for `CURRENT_REVISION` in the second:

```sh
docker compose exec dimsum dimsum control --socket /run/dimsum/control.sock settings
docker compose exec dimsum dimsum control --socket /run/dimsum/control.sock patch settings '{"revision":"CURRENT_REVISION","edits":[{"path":["admin","allowed_hosts"],"value":["localhost:18080","127.0.0.1:18080"]}]}'
```

Keep any existing allowed hosts in that list if you still use them. This example
makes DNS available on port 1053 and the dashboard available at
`http://localhost:18080`. Test it with
`dig @127.0.0.1 -p 1053 example.com`. Ordinary router and system DNS settings
expect port 53; a custom port is useful for testing, not a drop-in LAN DNS server.
For LAN service, resolve the port-53 conflict and use `DIMSUM_DNS_BIND=0.0.0.0`
or the Mac's LAN address.

Some Docker Desktop versions fail to forward DNS through loopback-only published
ports. If `DIMSUM_DNS_BIND=127.0.0.1` times out, use the default `0.0.0.0` binding
and restrict access with your firewall. The default configuration uses that
LAN-capable binding.

`DIMSUM_ADMIN_BIND` defaults to `127.0.0.1`. Changing it exposes administration
on the selected interface; use the [deployment access controls](deployment.md#https-and-access-controls)
when enabling remote access. These variables change host port publishing, not
the container's configured listeners.

Keep **Docker Desktop running and the Mac awake** whenever devices rely on it
for DNS. Sleep, shutdown, or quitting Docker Desktop interrupts service. The
restart policy can restart the container when Docker is running; it cannot wake
the Mac or launch Docker Desktop itself.

Docker Desktop's forwarding can hide original client source addresses. Query
history and per-device identification may show a shared forwarding address
instead of each LAN device. DHCP is unsupported: the container lacks direct LAN
DHCP broadcasts and link-layer access. Compose sets
`DIMSUM_DEPLOYMENT=docker-desktop` so the backend reports that reason through the
dashboard, CLI, and MCP. Use your router for DHCP, or run dimsum on Linux with
direct LAN access. See [DHCP containers](dhcp.md#containers).

### Status, logs, and command-line control

```sh
docker compose ps --all
docker compose logs --tail=100 dimsum
docker compose exec dimsum dimsum control --socket /run/dimsum/control.sock diagnostics
docker compose exec dimsum dimsum control --socket /run/dimsum/control.sock settings
docker compose exec dimsum dimsum control --socket /run/dimsum/control.sock help
```

The local socket commands use the shared control API and do not require a saved
HTTP login. For password recovery, run:

```sh
docker compose exec -T dimsum dimsum control --socket /run/dimsum/control.sock password @-
```

Enter `{"password":"YOUR_NEW_PASSWORD"}` and finish input with Ctrl-D. See
[recovering access](deployment.md#recovering-access) for credential behavior.

### Full backup and upgrades

The dashboard's configuration backup does not include query history. For a full
backup, stop the service so its databases and recovery files are consistent,
then copy both persistent directories as tar archives:

```sh
umask 077
backup="$HOME/dimsum-backup-$(date +%Y%m%d-%H%M%S)"
mkdir -m 700 "$backup"
cp compose.yaml "$backup/compose.yaml"
if [ -f .env ]; then cp .env "$backup/.env"; fi
docker compose stop dimsum
docker compose cp dimsum:/etc/dimsum/. - > "$backup/config.tar"
docker compose cp dimsum:/var/lib/dimsum/. - > "$backup/state.tar"
docker compose up -d --wait
tar -tf "$backup/config.tar"
tar -tf "$backup/state.tar"
```

`docker compose stop` retains the container so `docker compose cp` can read it
while stopped. Destination `-` streams a tar archive; the archives include the
full directories, including secrets and database sidecars. Check that both copy
commands succeed and the archives can be listed before relying on the backup.
Keep them private. A restore must replace both directories with the service
stopped and preserve container ownership (UID/GID 65532); do not merge a stale
database backup into live state.

After backing up, upgrade with:

```sh
docker compose pull
docker compose up -d --wait
docker compose ps
```

For a pinned image, update `DIMSUM_IMAGE` in `.env` first. These commands retain
configuration, credentials, and history in the named volumes.

### Stop or remove

`docker compose stop` stops the service while retaining its container and data.
`docker compose down` removes the container and network but **retains named
volumes**; `docker compose up -d --wait` can reuse them.

Only when you intend to delete all saved configuration, credentials, and history,
run `docker compose down --volumes`. This explicitly deletes the named volumes.

## Native macOS executable

Native release archives target **Darwin arm64** (Apple silicon) and **Darwin
amd64** (Intel), named `dimsum_VERSION_darwin_ARCH.tar.gz`. Download the matching
archive from [Releases](https://github.com/richkeenan/dimsum/releases) once a
release containing these targets is published, or follow the
[source build instructions](../README.md#build-from-source).

The executable is unsigned and not notarized. There is no launchd integration
or macOS installer script; `install.sh` and the systemd service are for Linux.
Run the native server in the foreground. From the extracted archive or repository
root, create a short private configuration directory:

```sh
mkdir -p "$HOME/.dimsum"
chmod 700 "$HOME/.dimsum"
cp deploy/dimsum.macos.yaml "$HOME/.dimsum/dims.yaml"
chmod 600 "$HOME/.dimsum/dims.yaml"
./dimsum serve -config "$HOME/.dimsum/dims.yaml" -state "$HOME/.dimsum/recovery"
```

Use `./dist/dimsum` instead of `./dimsum` for the source build. Copy the example
only for initial setup so you do not overwrite saved configuration. Relative
paths in the example resolve beside `dims.yaml`; keeping that directory short
also avoids macOS Unix socket path-length limits.

Open <http://localhost:8080> and sign in with **`admin`** on first startup. The
native example listens for DNS only at **127.0.0.1:5353**. Test it with:

```sh
dig @127.0.0.1 -p 5353 example.com
./dimsum control --socket "$HOME/.dimsum/run/control.sock" diagnostics
```

This is a local test listener, not system or LAN DNS on port 53. Setting your
Mac's DNS server to `127.0.0.1` does not select port 5353. The example does not
change system DNS settings. DHCP serving is unsupported on native macOS. Stop
the foreground process with Ctrl-C; it must remain running and the Mac awake
to answer queries.
