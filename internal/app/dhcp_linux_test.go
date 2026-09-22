//go:build linux

package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLinuxServiceDHCPIPv4Wildcard(t *testing.T) {
	if os.Getenv("DIMSUM_DHCP_ISOLATED_TEST") != "1" {
		t.Skip("requires isolated Linux namespace and socket capabilities")
	}
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command("ip", args...).CombinedOutput()
		require.NoError(t, err, "%s", out)
	}
	run("link", "set", "lo", "up")
	run("link", "add", "dhcp-app-server", "type", "veth", "peer", "name", "dhcp-app-client")
	t.Cleanup(func() {
		out, err := exec.Command("ip", "link", "del", "dhcp-app-server").CombinedOutput()
		assert.NoError(t, err, "%s", out)
	})
	run("addr", "add", "192.0.2.2/24", "dev", "dhcp-app-server")
	run("link", "set", "dhcp-app-server", "up")
	run("link", "set", "dhcp-app-client", "up")
	for _, initial := range []bool{true, false} {
		t.Run(fmt.Sprintf("startup-enabled-%t", initial), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			text := fmt.Sprintf("version: 1\ndns:\n  listen: [0.0.0.0:53]\n  upstreams: [127.0.0.1:9]\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: data\n  secrets_dir: secrets\ndhcp:\n  enabled: %t\n  interface: dhcp-app-server\n  server_ip: 192.0.2.2\n  subnet: 192.0.2.0/24\n  gateway: 192.0.2.1\n  range_start: 192.0.2.100\n  range_end: 192.0.2.110\n  lease_seconds: 3600\n  local_domain: home.arpa\n", initial)
			require.NoError(t, os.WriteFile(path, []byte(text), 0600))
			store, err := config.OpenStore(context.Background(), path, path+".state", config.StoreOptions{Offline: true})
			require.NoError(t, err)
			service := new(Service)
			require.NoError(t, service.StartManaged(context.Background(), store))
			defer service.Close()
			if !initial {
				document, err := config.Parse([]byte(text))
				require.NoError(t, err)
				candidate, err := document.Upsert([]config.Edit{{Path: []string{"dhcp", "enabled"}, Value: true}})
				require.NoError(t, err)
				_, err = store.Save(context.Background(), store.Snapshot().Revision(), candidate)
				require.NoError(t, err)
			}
			require.Eventually(t, func() bool { return service.DHCPStatus().State == "running" }, 5*time.Second, 10*time.Millisecond, "%+v", service.DHCPStatus())
			assert.Equal(t, store.Snapshot().Generation(), service.DHCPStatus().AppliedGeneration)
			assert.True(t, service.Ready())
			assert.NoError(t, service.Err())
			for _, network := range []string{"udp", "tcp"} {
				q := new(dns.Msg)
				q.SetQuestion("1.0.0.10.in-addr.arpa.", dns.TypePTR)
				response, _, err := (&dns.Client{Net: network, Timeout: time.Second}).Exchange(q, net.JoinHostPort("192.0.2.2", "53"))
				require.NoError(t, err)
				assert.Equal(t, dns.RcodeNameError, response.Rcode)
			}
			response, err := (&http.Client{Timeout: time.Second}).Get("http://" + service.Addresses().Admin + "/health/ready")
			require.NoError(t, err)
			defer response.Body.Close()
			assert.Equal(t, http.StatusOK, response.StatusCode)
		})
	}
}
