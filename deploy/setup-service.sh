#!/bin/sh
# Shared by the archive installer and Debian maintainer scripts.
dimsum_check() {
    for tool in systemctl systemd-sysusers runuser ss curl flock; do
        command -v "$tool" >/dev/null || { echo "Install $tool and try again." >&2; return 1; }
    done
    systemctl show-environment >/dev/null 2>&1 || { echo 'A running systemd service manager is required.' >&2; return 1; }
    if [ ! -e /etc/dimsum/dimsum.yaml ]; then
        occupied=$(ss -H -lntup '( sport = :53 or sport = :8080 )') || return 1
        if [ -n "$occupied" ]; then
            echo 'DNS port 53 or dashboard port 8080 is already in use:' >&2
            printf '%s\n' "$occupied" >&2
            echo 'Free these ports or configure /etc/dimsum/dimsum.yaml with different listeners, then retry.' >&2
            return 1
        fi
    fi
}

dimsum_prepare() {
    systemd-sysusers /usr/lib/sysusers.d/dimsum.conf || return 1
    install -d -o dimsum -g dimsum -m 0750 /etc/dimsum /var/lib/dimsum || return 1
    install -d -o dimsum -g dimsum -m 0700 /etc/dimsum/secrets || return 1
    if [ ! -e /etc/dimsum/dimsum.yaml ]; then
        install -o dimsum -g dimsum -m 0600 /usr/lib/dimsum/dimsum.example.yaml /etc/dimsum/dimsum.yaml || return 1
    fi
    runuser -u dimsum -- /usr/bin/dimsum validate -config /etc/dimsum/dimsum.yaml >/dev/null
}

dimsum_ready() {
    attempts=${DIMSUM_HEALTH_ATTEMPTS:-30}
    i=0
    while [ "$i" -lt "$attempts" ]; do
        # The local control listener exposes the same readiness endpoint. This
        # also works when an existing installation uses a different web port.
        if [ -n "${DIMSUM_HEALTH_URL:-}" ]; then
            if curl -fsS --noproxy '*' --max-time 2 "$DIMSUM_HEALTH_URL" >/dev/null 2>&1; then return 0; fi
        elif curl -fsS --noproxy '*' --max-time 2 --unix-socket "${DIMSUM_CONTROL_SOCKET:-/run/dimsum/control.sock}" http://localhost/health/ready >/dev/null 2>&1; then
            return 0
        fi
        i=$((i + 1))
        [ "$i" -ge "$attempts" ] || sleep 1
    done
    echo 'dimsum did not become ready. See: journalctl -u dimsum -n 50' >&2
    return 1
}

dimsum_start() {
    systemctl daemon-reload || return 1
    systemctl restart dimsum || return 1
    dimsum_ready || return 1
    systemctl enable dimsum || return 1
}

dimsum_summary() {
    echo 'dimsum is running and enabled at boot.'
    if [ "${fresh:-false}" = true ]; then
        address=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") {print $(i+1); exit}}')
        [ -n "$address" ] || address=$(hostname -I | awk '{print $1}')
        echo "Dashboard: http://${address:-localhost}:8080/"
        echo 'Sign in with admin, then change your password in Settings.'
        echo 'Choose subscriptions in Filter lists, then set your router DNS to this server.'
    else
        echo 'Your dashboard address, configuration, credentials and history are unchanged.'
    fi
}
