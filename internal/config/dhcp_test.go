package config

import (
	"github.com/richkeenan/dimsum/internal/dhcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/netip"
	"testing"
	"time"
)

func TestDHCPConfigDefaultsAndEdits(t *testing.T) {
	source := []byte("# keep me\nversion: 1\ndns:\n  listen: [127.0.0.1:5353]\nadmin:\n  listen: 127.0.0.1:8080\npaths:\n  data_dir: data\n  secrets_dir: secrets\n")
	d, err := Parse(source)
	require.NoError(t, err)
	assert.False(t, d.Config().DHCP.Enabled)
	d, err = d.Upsert([]Edit{{Path: []string{"dhcp", "enabled"}, Value: false}, {Path: []string{"dhcp", "interface"}, Value: "eth0"}})
	require.NoError(t, err)
	assert.Contains(t, string(d.Bytes()), "# keep me")
	_, err = d.Upsert([]Edit{{Path: []string{"dhcp", "enabled"}, Value: true}})
	assert.Error(t, err)
	_, err = Parse(append(source, []byte("dhcp:\n  test_port: 1067\n")...))
	assert.Error(t, err)
}

func TestDHCPCandidateMustReconcileHeldOwnership(t *testing.T) {
	source := []byte("version: 1\ndns:\n  listen: [0.0.0.0:53]\nadmin:\n  listen: 127.0.0.1:8080\npaths:\n  data_dir: data\n  secrets_dir: secrets\ndhcp:\n  enabled: true # retained comment\n  interface: eth0\n  server_ip: 192.0.2.2\n  subnet: 192.0.2.0/24\n  gateway: 192.0.2.1\n  range_start: 192.0.2.100\n  range_end: 192.0.2.199\n  lease_seconds: 86400\n  local_domain: home.arpa\n")
	d, err := Parse(source)
	require.NoError(t, err)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	e, err := dhcp.NewEngine(d.Config().DHCP, 1, func() time.Time { return now })
	require.NoError(t, err)
	l := dhcp.Lease{Identity: "mac:" + string([]byte{2, 0, 0, 0, 0, 1}), MAC: [6]byte{2, 0, 0, 0, 0, 1}, Address: netip.MustParseAddr("192.0.2.100"), State: dhcp.Bound, Expiry: now.Add(time.Hour), HoldUntil: now.Add(time.Hour)}
	require.NoError(t, e.Restore([]dhcp.Lease{l}, now))
	candidate, err := d.Upsert([]Edit{{Path: []string{"dhcp", "range_start"}, Value: "192.0.2.150"}, {Path: []string{"dhcp", "gateway"}, Value: "192.0.2.100"}})
	require.NoError(t, err)
	assert.Contains(t, string(candidate.Bytes()), "# retained comment")
	assert.Error(t, e.Apply(candidate.Config().DHCP, 2))
	assert.Equal(t, []dhcp.Lease{l}, e.DurableLeases())
}

func TestDHCPRequiresDNSOnServerPort53(t *testing.T) {
	c := Default()
	c.DHCP = dhcp.Settings{Enabled: true, Interface: "eth0", ServerIP: "192.0.2.2", Subnet: "192.0.2.0/24", Gateway: "192.0.2.1", RangeStart: "192.0.2.100", RangeEnd: "192.0.2.199", LeaseSeconds: 86400, LocalDomain: "home.arpa"}
	for _, listen := range []string{"127.0.0.1:53", "192.0.2.2:5353", "[::1]:53", "[2001:db8::1]:53", "[::]:5353"} {
		c.DNS.Listen = []string{listen}
		assert.Error(t, Validate(c), listen)
	}
	for _, listen := range []string{"192.0.2.2:53", "0.0.0.0:53", "[::]:53"} {
		c.DNS.Listen = []string{listen}
		assert.NoError(t, Validate(c), listen)
	}
}

func TestDHCPEditRequiresDisableForNetworkChange(t *testing.T) {
	source := []byte("version: 1\ndns:\n  listen: [0.0.0.0:53]\nadmin:\n  listen: 127.0.0.1:8080\npaths:\n  data_dir: data\n  secrets_dir: secrets\ndhcp:\n  enabled: true # preserve\n  interface: eth0\n  server_ip: 192.0.2.2\n  subnet: 192.0.2.0/24\n  gateway: 192.0.2.1\n  range_start: 192.0.2.100\n  range_end: 192.0.2.199\n  lease_seconds: 86400\n  local_domain: home.arpa\n")
	d, err := Parse(source)
	require.NoError(t, err)
	edit := []Edit{{Path: []string{"dhcp", "interface"}, Value: "eth1"}}
	_, err = d.Edit(edit)
	assert.Error(t, err)
	_, err = d.Upsert(edit)
	assert.Error(t, err)
	d, err = d.Upsert([]Edit{{Path: []string{"dhcp", "enabled"}, Value: false}})
	require.NoError(t, err)
	d, err = d.Upsert(edit)
	require.NoError(t, err)
	assert.Contains(t, string(d.Bytes()), "# preserve")
}
