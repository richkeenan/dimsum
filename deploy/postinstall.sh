#!/bin/sh
set -eu
case "${1:-}" in configure) ;; *) exit 0 ;; esac
# shellcheck source=deploy/setup-service.sh
. /usr/lib/dimsum/setup-service.sh
fresh=false
[ -e /etc/dimsum/dimsum.yaml ] || fresh=true
dimsum_check
dimsum_prepare
if [ -z "${2:-}" ]; then
    dimsum_start
    dimsum_summary
else
    # Package upgrades preserve the administrator's boot and running state.
    systemctl daemon-reload
    enabled=$(systemctl is-enabled dimsum 2>/dev/null || true)
    case "$enabled" in
        masked|masked-runtime) ;;
        *)
            if systemctl is-active --quiet dimsum; then
                systemctl restart dimsum
                dimsum_ready
            fi
            ;;
    esac
    echo 'dimsum updated; existing service enablement and configuration preserved.'
fi
