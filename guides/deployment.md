# Deployment

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
sudo -u dimsum dimsum control diagnostics
```

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
sudo -u dimsum dimsum control password @-
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

The repository supplies a Dockerfile and Compose definition for native Linux.
The configuration and state directories must be writable by UID/GID 65532.
Mount the configuration directory rather than just its YAML file because saves
use atomic rename. The image contains no initial configuration or credentials.

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
Container images are built locally; release automation publishes binaries and
Debian packages, not a public container registry image.

The supplied Compose service uses host networking and retains NET_BIND_SERVICE.
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
