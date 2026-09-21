#!/bin/sh
# Download a verified release; all machine setup lives in the versioned archive.
set -eu
main() {
    version=latest
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --version) [ "$#" -ge 2 ] || { echo '--version requires a tag' >&2; exit 2; }; version=$2; shift ;;
            -h|--help) echo 'Usage: sudo sh install.sh [--version vX.Y.Z]'; exit 0 ;;
            *) echo "Unknown option: $1" >&2; exit 2 ;;
        esac
        shift
    done
    [ "$(uname -s)" = Linux ] || { echo 'The installer requires Linux with systemd.' >&2; exit 1; }
    case "$(uname -m)" in
        x86_64) arch=amd64 ;;
        aarch64|arm64) arch=arm64 ;;
        *) echo 'Use 64-bit Linux on an amd64 server or arm64 Raspberry Pi.' >&2; exit 1 ;;
    esac
    [ "$(id -u)" = 0 ] || { echo 'Run this installer with sudo.' >&2; exit 1; }
    for tool in curl tar sha256sum mktemp; do
        command -v "$tool" >/dev/null || { echo "Install $tool and try again." >&2; exit 1; }
    done
    repo=https://github.com/richkeenan/dimsum
    if [ "$version" = latest ]; then
        latest=$(curl -fsSL --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 60 -o /dev/null -w '%{url_effective}' "$repo/releases/latest") || {
            echo "No release could be downloaded. Check $repo/releases and your connection." >&2; exit 1;
        }
        version=${latest##*/}
    fi
    # Tags become URL paths and filenames; accept only version-shaped tags.
    printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9][A-Za-z0-9.-]*)?$' || {
        echo "Invalid release tag: $version (expected vX.Y.Z)." >&2; exit 2;
    }
    stage=$(mktemp -d)
    trap 'rm -rf "$stage"' EXIT
    trap 'exit 1' HUP INT TERM
    archive="dimsum_${version#v}_linux_${arch}.tar.gz"
    echo "Downloading dimsum $version for linux/$arch..."
    for file in "$archive" checksums.txt; do
        curl -fsSL --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 300 --retry 3 \
            -o "$stage/$file" "$repo/releases/download/$version/$file" || {
            echo "Could not download $file. Check $repo/releases/tag/$version." >&2; exit 1;
        }
    done
    awk -v file="$archive" '$2 == file { print }' "$stage/checksums.txt" > "$stage/selected.sha256"
    [ "$(wc -l < "$stage/selected.sha256")" -eq 1 ] || { echo 'Missing or duplicate archive checksum.' >&2; exit 1; }
    (cd "$stage" && sha256sum -c selected.sha256)
    mkdir "$stage/unpacked"
    tar -xzf "$stage/$archive" -C "$stage/unpacked" --no-same-owner
    sh "$stage/unpacked/scripts/install-service.sh" "$stage/unpacked"
}
# Read the complete script before doing any work when invoked through curl | sh.
main "$@"
