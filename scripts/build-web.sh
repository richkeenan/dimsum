#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
npm --prefix web ci
npm --prefix web run build
test -s internal/webassets/dist/index.html
