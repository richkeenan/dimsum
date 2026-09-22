package dhcp

import (
	"net"
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupDetectsNetworkWithoutEnablingOrReplacingSavedValues(t *testing.T) {
	links := []setupAddress{{Interface: "eth0", Prefix: netip.MustParsePrefix("192.0.2.2/24"), Fixed: "yes"}}
	routes := []setupRoute{{Interface: "eth0", Gateway: netip.MustParseAddr("192.0.2.1"), Metric: 100}}
	v := suggestSetup(Settings{}, links, routes)
	assert.False(t, v.Config.Enabled)
	assert.Equal(t, "eth0", v.Config.Interface)
	assert.Equal(t, "192.0.2.2", v.Config.ServerIP)
	assert.Equal(t, "192.0.2.0/24", v.Config.Subnet)
	assert.Equal(t, "192.0.2.1", v.Config.Gateway)
	assert.Equal(t, "yes", v.FixedAddress)
	assert.Contains(t, v.Suggested, "range_start")
	c := v.Config
	c.Enabled = true
	require.NoError(t, ValidateSettings(c))
	assert.Equal(t, 86400, c.LeaseSeconds)
	assert.Equal(t, "home.arpa", c.LocalDomain)
	c.Enabled = false
	c.LeaseSeconds = 7200
	c.LocalDomain = "lan.example"
	c.RangeStart, c.RangeEnd = "192.0.2.30", "192.0.2.60"
	saved := suggestSetup(c, links, routes)
	assert.Equal(t, c, saved.Config)
	assert.Empty(t, saved.Suggested)
}

func TestSetupAmbiguityDoesNotInventNetwork(t *testing.T) {
	links := []setupAddress{
		{Interface: "eth0", Prefix: netip.MustParsePrefix("192.0.2.2/24")},
		{Interface: "eth1", Prefix: netip.MustParsePrefix("198.51.100.2/24")},
	}
	routes := []setupRoute{
		{Interface: "eth0", Gateway: netip.MustParseAddr("192.0.2.1"), Metric: 100},
		{Interface: "eth1", Gateway: netip.MustParseAddr("198.51.100.1"), Metric: 100},
	}
	v := suggestSetup(Settings{}, links, routes)
	assert.Empty(t, v.Config.Interface)
	assert.Empty(t, v.Config.RangeStart)
	assert.NotEmpty(t, v.Message)
	// A saved interface resolves ambiguity, and a lower route metric does too.
	v = suggestSetup(Settings{Interface: "eth1"}, links, routes)
	assert.Equal(t, "198.51.100.2", v.Config.ServerIP)
	routes[0].Metric = 50
	v = suggestSetup(Settings{}, links, routes)
	assert.Equal(t, "192.0.2.2", v.Config.ServerIP)
	// Existing off-network values must never be mixed with the detected LAN.
	v = suggestSetup(Settings{ServerIP: "203.0.113.5", Subnet: "203.0.113.0/24"}, links, routes)
	assert.Empty(t, v.Config.Interface)
	assert.Empty(t, v.Config.Gateway)
	assert.Equal(t, "203.0.113.5", v.Config.ServerIP)
}

func TestSetupRangeExcludesLocalAddressesAndReservations(t *testing.T) {
	links := []setupAddress{
		{Interface: "eth0", Prefix: netip.MustParsePrefix("192.0.2.150/24"), Fixed: "no"},
		{Interface: "eth1", Prefix: netip.MustParsePrefix("192.0.2.180/24")},
	}
	routes := []setupRoute{{Interface: "eth0", Gateway: netip.MustParseAddr("192.0.2.200")}}
	v := suggestSetup(Settings{Reservations: []Reservation{{ID: "printer", MAC: "02:00:00:00:00:01", Address: "192.0.2.220"}}}, links, routes)
	assert.Equal(t, "no", v.FixedAddress)
	start := netip.MustParseAddr(v.Config.RangeStart)
	end := netip.MustParseAddr(v.Config.RangeEnd)
	for _, address := range []string{"192.0.2.150", "192.0.2.180", "192.0.2.200", "192.0.2.220"} {
		a := netip.MustParseAddr(address)
		assert.False(t, a.Compare(start) >= 0 && a.Compare(end) <= 0, address)
	}
	v.Config.Enabled = true
	require.NoError(t, ValidateSettings(v.Config))
}

func TestSetupCollectsExcludedInterfaceAddressesForPoolExclusion(t *testing.T) {
	interfaces := []net.Interface{
		{Name: "eth0", Flags: net.FlagUp, HardwareAddr: net.HardwareAddr{2, 0, 0, 0, 0, 1}, MTU: 1500},
		{Name: "lo", Flags: net.FlagUp | net.FlagLoopback, MTU: 65536},
		{Name: "docker0", Flags: net.FlagUp, HardwareAddr: net.HardwareAddr{2, 0, 0, 0, 0, 2}, MTU: 1500},
	}
	var addresses []setupAddress
	for i, text := range []string{"192.0.2.2/24", "192.0.2.150/32", "192.0.2.180/24"} {
		ip, network, err := net.ParseCIDR(text)
		require.NoError(t, err)
		network.IP = ip
		addresses = append(addresses, setupAddresses(interfaces[i], []net.Addr{network}, "")...)
	}
	v := suggestSetup(Settings{}, addresses, []setupRoute{
		{Interface: "eth0", Gateway: netip.MustParseAddr("192.0.2.1"), Metric: 100},
		{Interface: "docker0", Gateway: netip.MustParseAddr("192.0.2.1"), Metric: 0},
	})
	assert.Equal(t, "eth0", v.Config.Interface)
	start, end := netip.MustParseAddr(v.Config.RangeStart), netip.MustParseAddr(v.Config.RangeEnd)
	for _, text := range []string{"192.0.2.150", "192.0.2.180"} {
		a := netip.MustParseAddr(text)
		assert.False(t, a.Compare(start) >= 0 && a.Compare(end) <= 0, text)
	}
}

func TestSetupSmallSubnetsAndPartialPools(t *testing.T) {
	links := []setupAddress{{Interface: "eth0", Prefix: netip.MustParsePrefix("192.0.2.2/30")}}
	routes := []setupRoute{{Interface: "eth0", Gateway: netip.MustParseAddr("192.0.2.1")}}
	v := suggestSetup(Settings{}, links, routes)
	assert.Empty(t, v.Config.RangeStart)
	assert.NotEmpty(t, v.Message)
	links[0].Prefix = netip.MustParsePrefix("192.0.2.2/24")
	v = suggestSetup(Settings{RangeStart: "192.0.2.20"}, links, routes)
	assert.Equal(t, "192.0.2.20", v.Config.RangeStart)
	assert.Empty(t, v.Config.RangeEnd, "do not invent the other half of a saved pool")
}

func TestSetupDoesNotCombineIncompatiblePartialConfiguration(t *testing.T) {
	links := []setupAddress{{Interface: "eth0", Prefix: netip.MustParsePrefix("192.0.2.2/24")}}
	routes := []setupRoute{{Interface: "eth0", Gateway: netip.MustParseAddr("192.0.2.1")}}
	for _, saved := range []Settings{
		{Gateway: "198.51.100.1"},
		{RangeStart: "198.51.100.100", RangeEnd: "198.51.100.200"},
		{RangeStart: "192.0.2.1", RangeEnd: "192.0.2.10"},
	} {
		v := suggestSetup(saved, links, routes)
		assert.Empty(t, v.Config.Subnet)
		assert.Empty(t, v.Config.ServerIP)
		require.NoError(t, ValidateSettings(v.Config))
	}
}

func TestSetupReadsOnlyUsableDefaultRoutes(t *testing.T) {
	routes := parseSetupRoutes(strings.NewReader(`Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT
eth0 00000000 010200C0 0003 0 0 100 00000000 0 0 0
eth0 000200C0 00000000 0001 0 0 0 00FFFFFF 0 0 0
eth1 00000000 016433C6 0203 0 0 10 00000000 0 0 0
bad malformed row
`))
	require.Len(t, routes, 1)
	assert.Equal(t, "eth0", routes[0].Interface)
	assert.Equal(t, "192.0.2.1", routes[0].Gateway.String())
	assert.Equal(t, uint64(100), routes[0].Metric)
}
