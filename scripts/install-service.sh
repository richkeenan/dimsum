#!/bin/sh
# Install a checksum-verified, extracted release archive.
set -eu
[ "$(id -u)" = 0 ] || { echo 'Run with sudo.' >&2; exit 1; }
[ "$#" -eq 1 ] || { echo 'Usage: install-service.sh EXTRACTED_ARCHIVE' >&2; exit 2; }
archive=$(CDPATH='' cd -- "$1" && pwd)
# shellcheck source=deploy/setup-service.sh
. "$archive/deploy/setup-service.sh"
dimsum_check
exec 9>/usr/bin/dimsum.upgrade.lock
flock -n 9 || { echo 'Another dimsum installation is running.' >&2; exit 1; }
if command -v dpkg-query >/dev/null; then
    package_state=$(dpkg-query -W -f='${db:Status-Status}' dimsum 2>/dev/null || true)
    case "$package_state" in
        ''|not-installed|config-files) ;;
        *) echo 'dimsum is managed by apt. Upgrade with: sudo apt install ./dimsum_VERSION_ARCH.deb' >&2; exit 1 ;;
    esac
fi
fresh=false
[ -e /etc/dimsum/dimsum.yaml ] || fresh=true
previous=false
replaced=false
next=
cleanup() {
    result=$?
    trap - EXIT
    if [ "$replaced" = true ] && [ "$result" -ne 0 ]; then
        if [ "$previous" = true ]; then
            echo 'Installation failed; restoring the previous executable.' >&2
            cp -p /usr/bin/dimsum.previous "$next" && mv -f "$next" /usr/bin/dimsum
            systemctl restart dimsum || echo 'Could not restart the previous version; inspect journalctl -u dimsum.' >&2
        else
            systemctl stop dimsum || true
        fi
    fi
    [ -z "$next" ] || rm -f "$next"
    exit "$result"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
"$archive/dimsum" version >/dev/null
install -d /usr/lib/sysusers.d /usr/lib/systemd/system /usr/lib/dimsum
install -m 0644 "$archive/deploy/dimsum.sysusers" /usr/lib/sysusers.d/dimsum.conf
install -m 0644 "$archive/deploy/dimsum.example.yaml" /usr/lib/dimsum/dimsum.example.yaml
# Existing units may contain installation-specific settings.
if [ ! -e /usr/lib/systemd/system/dimsum.service ] && [ ! -e /etc/systemd/system/dimsum.service ]; then
    install -m 0644 "$archive/deploy/dimsum.service" /usr/lib/systemd/system/dimsum.service
fi
next=$(mktemp /usr/bin/dimsum.next.XXXXXX)
install -m 0755 "$archive/dimsum" "$next"
if [ -e /usr/bin/dimsum ]; then
    cp -p /usr/bin/dimsum /usr/bin/dimsum.previous
    previous=true
fi
replaced=true
mv -f "$next" /usr/bin/dimsum
dimsum_prepare
dimsum_start
replaced=false
dimsum_summary
