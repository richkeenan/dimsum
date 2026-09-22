# Deployment

For a Mac, see the [macOS guide](macos.md) for Docker Desktop setup and native
foreground builds. The Linux service instructions below use systemd.

## Native Linux service

The installer supports systemd on Linux amd64 and arm64. It checks ports 53 and
8080 and installs the service. Sign in with **`admin`** on a fresh installation.
You can change your password in Settings. Upgrades preserve existing credentials.

The native defaults use these paths:

| Purpose | Path |
| --- | --- |
| Configuration | `/etc/dimsum/dimsum.yaml` |
| Restricted secrets | `/etc/dimsum/secrets/` |
| Statistics, downloaded lists, DHCP state | `/var/lib/dimsum/data/` |
| Recovery state | `/var/lib/dimsum/recovery/` |
| Local control socket | `/run/dimsum/control.sock` |

If another resolver already owns port 53, choose which service should handle DNS
before installing. Do not disable an existing resolver without a replacement
plan. For custom listeners, prepare the configuration first. The installer does
not alter your router settings.

Inspect service failures with `journalctl -u dimsum -n 50`. Check readiness with:

```sh
dimsum login
dimsum control diagnostics
```

## Command-line login

The system service runs as the unprivileged `dimsum` account. Installation needs
sudo to create the service and install system files; systemd grants the service
`CAP_NET_BIND_SERVICE` so it can listen on DNS port 53.

Run `dimsum login` from your normal account. Enter the dashboard password at the
hidden prompt. The default server is `http://127.0.0.1:8080`; for a different
address, use `dimsum login --server https://dns.example.net`. Subsequent
`dimsum control` commands use this saved server without sudo.

Login prints the server address before asking for your password. Check that it
matches your dashboard: another application on port 8080 can reject a login too.
Login connects to a running server; it does not start one. Rebuilding dimsum
does not reset the server's saved password.

The CLI stores the server address and a revocable API token, not your password,
in `dimsum/credentials.json` under your user configuration directory. On Linux
this is `$XDG_CONFIG_HOME` or `~/.config`; on macOS the default is
`~/Library/Application Support`. The directory has mode 0700 and the file 0600.
For scripted login, use `--password-file PATH` or `--password-file -` for stdin.

Run `dimsum logout` to revoke the token and delete the saved connection before
logging in to another server. If the server is unavailable, logout keeps the
credentials so you can retry. You can also revoke the token named `dimsum CLI`
in **Settings → Agent access**. After revocation, run logout and log in again.

For local recovery or service scripts, use `dimsum control --socket PATH ...`
or set `DIMSUM_CONTROL_SOCKET`. An explicit socket takes precedence over the
saved HTTP connection and requires access to that socket.

## Optional initial password

Fresh installations create the default **`admin`** password on first startup.
No separate credential setup is required. To choose a different initial password,
you can optionally bootstrap one before starting the server:

```sh
dimsum bootstrap -config /path/to/dimsum.yaml -generate
```

The command returns JSON containing the random password once. It stores only a
salted password hash and refuses to replace an existing credential. Alternatively,
supply a password in an owner-only regular file:

```sh
dimsum bootstrap -config /path/to/dimsum.yaml -password-file /path/to/password
```

Use `-password-file -` to read from standard input. Remove the input file after
bootstrapping. Run bootstrap as the user that will run the service.

Startup preserves existing credentials, including passwords set through Settings
or the CLI. It does not reset them to the default.

### Recovering access

On a running native installation, use the local CLI to set a new password:

```sh
sudo -u dimsum dimsum control --socket /run/dimsum/control.sock password @-
```

Enter a JSON object such as `{"password":"YOUR_NEW_PASSWORD"}`, then end standard
input with Ctrl-D. Avoid placing real passwords in shell command arguments or
history. The operation revokes prior browser sessions. Existing API tokens are
managed separately in Agent access.

## HTTPS and access controls

For a reverse proxy on the same host, bind the admin service to loopback and
configure the exact public host (including a non-default port):

```yaml
admin:
  listen: 127.0.0.1:8080
  allowed_hosts: [dns.example.net]
  secure_cookies: true
```

Terminate HTTPS at your proxy and preserve the browser's Host and Origin headers
when forwarding to `http://127.0.0.1:8080`. Forward streaming responses without
buffering for events and MCP. `secure_cookies` marks cookies secure and makes
Origin validation expect HTTPS; it does not create a TLS listener. Restart the
service after changing the listener or secure-cookie setting.

Restrict DNS TCP/UDP 53 and admin access to intended networks. Do not expose an
open recursive resolver or the local control socket to the internet.

## Containers

The repository supplies a Dockerfile and [`deploy/compose.yaml`](../deploy/compose.yaml)
for native Linux. For Docker Desktop, use the separate
[`compose.desktop.yaml`](../deploy/compose.desktop.yaml) and [macOS guide](macos.md).
The configuration and state directories must be writable by UID/GID 65532.
Mount the configuration directory rather than just its YAML file because saves
use atomic rename. The image includes default configuration with no credentials;
fresh named volumes inherit its defaults and ownership. Bind mounts used by the
Linux Compose definition must be prepared explicitly as described below.

Build from a clean checkout. Supply provenance from that checkout:

```sh
export DIMSUM_CONFIG_DIR=/absolute/path/to/config
export DIMSUM_STATE_DIR=/absolute/path/to/state
export SOURCE_COMMIT=$(git rev-parse HEAD)
export SOURCE_DATE_EPOCH=$(git show -s --format=%ct HEAD)
docker compose -f deploy/compose.yaml build
```

Prepare a configuration in `$DIMSUM_CONFIG_DIR/dimsum.yaml` with `data_dir`
`/var/lib/dimsum/data`, `secrets_dir` `/etc/dimsum/secrets`, and control socket
`/run/dimsum/control.sock`. Then start and sign in with **`admin`**:

```sh
docker compose -f deploy/compose.yaml up -d
```

Source archives can also build: omit provenance variables if unknown. The build
will label them unrecorded; it does not require Git history in the Docker context.
Tagged release automation is configured to publish multi-platform Linux images
to `ghcr.io/richkeenan/dimsum`, alongside binaries, Debian packages, and the
Desktop Compose asset. Version image tags omit the release tag's leading `v`;
`latest` tracks stable releases. These images and the Desktop asset require a
future tagged release; a push to the branch does not publish a release. On first
publication, the package owner may need to set the GHCR package visibility to
**Public** before unauthenticated pulls work.

The native Linux Compose service uses host networking and retains NET_BIND_SERVICE.
Docker Desktop does not provide the same LAN interface behavior as native Linux.
See the [DHCP guide](dhcp.md) before adding DHCP privileges.

## Backups and upgrades

Configuration backups include secrets and should be stored privately. They do
not include query history or DHCP lease ownership. Preserve the entire data
directory through upgrades. For DHCP migrations, follow the lease-state procedure
in the [DHCP guide](dhcp.md#lease-state-and-migration).

For a Debian installation, download the correct architecture package and run
`sudo apt install ./dimsum_VERSION_ARCH.deb`. Package upgrades preserve service
enablement and only restart an active service. The archive installer rolls back
the executable if restart or readiness fails.
