#!/bin/sh
set -eu
case "${1:-}" in
    remove|deconfigure)
        systemctl disable --now dimsum
        ;;
esac
