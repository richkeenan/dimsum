#!/bin/sh
# Real release binary/package integration in a disposable Linux container.
# systemctl models lifecycle; all binary, filesystem and HTTP operations are real.
set -eu
test -f /.dockerenv || exit 1
mkdir -p /fixture/bin
export PATH="/fixture/bin:$PATH"
cat > /fixture/bin/systemctl <<'SH'
#!/bin/sh
set -eu
stop() {
    if [ -f /fixture/pid ]; then
        kill "$(cat /fixture/pid)" 2>/dev/null || true
        i=0
        while [ -S /run/dimsum/control.sock ] && [ "$i" -lt 50 ]; do sleep 0.1; i=$((i+1)); done
        rm -f /fixture/pid
    fi
}
case "$1" in
    restart)
        stop
        install -d -o dimsum -g dimsum -m 0700 /run/dimsum
        runuser -u dimsum -- /usr/bin/dimsum serve -config /etc/dimsum/dimsum.yaml -state /var/lib/dimsum/recovery >/fixture/server.log 2>&1 &
        echo "$!" > /fixture/pid
        ;;
    stop|disable) stop ;;
esac
SH
chmod +x /fixture/bin/systemctl
trap 'systemctl stop dimsum' EXIT
arch=$(dpkg --print-architecture)
set -- /packages/dimsum_*_"$arch".deb
test "$#" -eq 1
package=$1
dpkg -i "$package"
curl -fsS --unix-socket /run/dimsum/control.sock http://localhost/health/ready
curl -fsS http://127.0.0.1:8080/ > /fixture/dashboard
grep -qi '<!doctype html>' /fixture/dashboard
curl -fsS -H 'Content-Type: application/json' -d '{"password":"admin"}' http://127.0.0.1:8080/session > /fixture/login
echo '# preserved by package upgrade' >> /etc/dimsum/dimsum.yaml
cp /etc/dimsum/dimsum.yaml /fixture/config
cp -a /etc/dimsum/secrets /fixture/secrets
dpkg -i "$package"
cmp /fixture/config /etc/dimsum/dimsum.yaml
diff -r /fixture/secrets /etc/dimsum/secrets
curl -fsS --unix-socket /run/dimsum/control.sock http://localhost/health/ready
dpkg -r dimsum
test ! -e /usr/bin/dimsum
test -e /etc/dimsum/dimsum.yaml
diff -r /fixture/secrets /etc/dimsum/secrets

# The archive must include everything needed by the offline installer too.
mkdir /fixture/unpacked
tar -xzf /packages/dimsum_*_linux_"$arch".tar.gz -C /fixture/unpacked
rm -rf /etc/dimsum /var/lib/dimsum
sh /fixture/unpacked/scripts/install-service.sh /fixture/unpacked
curl -fsS --unix-socket /run/dimsum/control.sock http://localhost/health/ready
echo 'Real package/archive installation, dashboard, login, reinstall and removal passed.'
