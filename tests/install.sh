#!/bin/sh
# Run only inside the disposable installer test container.
set -eu
test -f /.dockerenv || { echo 'Run with tests/installer.Dockerfile' >&2; exit 1; }
mkdir -p /fixture/bin /fixture/release /fixture/archive
export PATH="/fixture/bin:$PATH"
export DIMSUM_HEALTH_ATTEMPTS=1
case "$(uname -m)" in x86_64) arch=amd64 ;; *) arch=arm64 ;; esac
cat > /fixture/bin/systemctl <<'SH'
#!/bin/sh
if [ -f /fixture/preserve-service ]; then
    case "$1" in
        is-active) exit 1 ;;
        is-enabled) cat /fixture/preserve-service; exit 1 ;;
        restart|enable) echo 'FAIL: package changed a stopped/disabled service' >&2; exit 1 ;;
    esac
fi
if [ "$1" = restart ] && [ -f /fixture/fail-start ] && grep -q new-build /usr/bin/dimsum; then exit 1; fi
exit 0
SH
cat > /fixture/bin/curl <<'SH'
#!/bin/sh
out=
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o|--output) out=$2; shift ;;
        https://*/releases/latest) echo 'https://github.com/richkeenan/dimsum/releases/tag/v1.2.3'; exit 0 ;;
        https://*/releases/download/*) url=$1 ;;
        http://localhost/health/ready)
            if [ -f /fixture/fail-health ] && grep -q new-build /usr/bin/dimsum; then exit 1; fi
            exit 0 ;;
    esac
    shift
done
cp "/fixture/release/${url##*/}" "$out"
SH
chmod +x /fixture/bin/*
make_release() {
    rm -rf /fixture/archive/*
    mkdir -p /fixture/archive/scripts /fixture/archive/deploy
    cp scripts/install-service.sh /fixture/archive/scripts/
    cp deploy/setup-service.sh deploy/dimsum.service deploy/dimsum.sysusers deploy/dimsum.example.yaml /fixture/archive/deploy/
    printf '#!/bin/sh\n# %s\nexit 0\n' "$1" > /fixture/archive/dimsum
    chmod +x /fixture/archive/dimsum
    tar -czf "/fixture/release/dimsum_1.2.3_linux_${arch}.tar.gz" -C /fixture/archive .
    (cd /fixture/release && sha256sum *.tar.gz > checksums.txt)
}
install_release() { sh install.sh --version v1.2.3; }

# Missing installer must fail this test before any host setup is attempted.
test -f install.sh || { echo 'FAIL: one-command installer is missing' >&2; exit 1; }
make_release old-build

# Port conflicts must fail before creating configuration or replacing binaries.
node -e 'require("node:http").createServer().listen(8080)' >/dev/null 2>&1 &
listener=$!
sleep 1
if install_release; then echo 'FAIL: occupied port accepted'; exit 1; fi
test ! -e /usr/bin/dimsum
test ! -e /etc/dimsum/dimsum.yaml
kill "$listener"
wait "$listener" 2>/dev/null || true

sh install.sh
test -x /usr/bin/dimsum
test "$(stat -c %U /etc/dimsum/dimsum.yaml)" = dimsum
test "$(stat -c %a /etc/dimsum/secrets)" = 700
grep -q '0.0.0.0:53' /etc/dimsum/dimsum.yaml
grep -q '0.0.0.0:8080' /etc/dimsum/dimsum.yaml
echo '# user configuration' >> /etc/dimsum/dimsum.yaml
echo credential > /etc/dimsum/secrets/test
echo history > /var/lib/dimsum/test
cp /etc/dimsum/dimsum.yaml /fixture/config-before

make_release new-build
touch /fixture/fail-health
if install_release; then echo 'FAIL: unhealthy upgrade accepted'; exit 1; fi
grep -q old-build /usr/bin/dimsum
rm /fixture/fail-health
touch /fixture/fail-start
if install_release; then echo 'FAIL: failed upgrade accepted'; exit 1; fi
grep -q old-build /usr/bin/dimsum
cmp /fixture/config-before /etc/dimsum/dimsum.yaml
rm /fixture/fail-start
install_release
grep -q new-build /usr/bin/dimsum
cmp /fixture/config-before /etc/dimsum/dimsum.yaml
test "$(cat /etc/dimsum/secrets/test)" = credential
test "$(cat /var/lib/dimsum/test)" = history

(
    exec 9>/usr/bin/dimsum.upgrade.lock
    flock -n 9
    if install_release; then echo 'FAIL: concurrent install accepted'; exit 1; fi
)

printf tampered >> "/fixture/release/dimsum_1.2.3_linux_${arch}.tar.gz"
if install_release; then echo 'FAIL: checksum mismatch accepted'; exit 1; fi
grep -q new-build /usr/bin/dimsum
if sh install.sh --version '../invalid'; then echo 'FAIL: invalid version accepted'; exit 1; fi

cat > /fixture/bin/uname <<'SH'
#!/bin/sh
if [ "$1" = -s ]; then echo Linux; exit 0; fi
echo unsupported
SH
chmod +x /fixture/bin/uname
if install_release; then echo 'FAIL: unsupported platform accepted'; exit 1; fi
rm /fixture/bin/uname

make_release old-build
cat > /fixture/bin/dpkg-query <<'SH'
#!/bin/sh
echo half-configured
SH
chmod +x /fixture/bin/dpkg-query
if install_release; then echo 'FAIL: package-owned binary overwritten'; exit 1; fi
grep -q new-build /usr/bin/dimsum
rm /fixture/bin/dpkg-query

# Debian setup uses the same defaults and preserves existing user data.
mkdir -p /usr/lib/dimsum /usr/lib/sysusers.d
cp deploy/setup-service.sh /usr/lib/dimsum/
cp deploy/dimsum.example.yaml /usr/lib/dimsum/
cp deploy/dimsum.sysusers /usr/lib/sysusers.d/dimsum.conf
sh deploy/postinstall.sh configure
cmp /fixture/config-before /etc/dimsum/dimsum.yaml
for state in disabled masked enabled; do
    echo "$state" > /fixture/preserve-service
    sh deploy/postinstall.sh configure 1.2.2
done
rm /fixture/preserve-service
rm -rf /etc/dimsum /var/lib/dimsum
sh deploy/postinstall.sh configure
test -f /etc/dimsum/dimsum.yaml
test "$(stat -c %U /etc/dimsum/dimsum.yaml)" = dimsum
echo 'Installer checks passed.'
