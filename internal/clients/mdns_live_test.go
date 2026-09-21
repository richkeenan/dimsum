package clients

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

// Explicitly opt-in: normal tests never query the LAN. Addresses are supplied
// at runtime and neither identities nor advertisements are written to artifacts.
func TestMDNSLive(t *testing.T) {
	input := os.Getenv("DIMSUM_MDNS_TEST_CLIENTS")
	if input == "" {
		t.Skip("set DIMSUM_MDNS_TEST_CLIENTS for an authorized LAN probe")
	}
	var addresses []netip.Addr
	for _, s := range strings.Split(input, ",") {
		a, e := netip.ParseAddr(s)
		require.NoError(t, e)
		addresses = append(addresses, a)
	}
	view, e := NewView(Settings{MDNS: MDNSSettings{Enabled: true, Interfaces: strings.Fields(os.Getenv("DIMSUM_MDNS_TEST_INTERFACES"))}}, nil, nil)
	require.NoError(t, e)
	m := New(func() *View { return view })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	for _, a := range addresses {
		m.Observe(a)
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	<-timer.C
	names, classified := 0, 0
	for _, a := range addresses {
		n := m.Get(a)
		if n.Name != "" {
			names++
		}
		if n.Device != nil && n.Device.Category != "unknown" {
			classified++
		}
	}
	d := m.Diagnostics()
	assert.True(t, d.Running, "multicast transport must start")
	assert.Greater(t, names, 0, "at least one supplied client should be discoverable")
	t.Logf("Supplied=%d named=%d classified=%d records=%d queries=%d responses=%d dropped=%d transport_errors=%d", len(addresses), names, classified, d.Records, d.Queries, d.Responses, d.Dropped, len(d.Errors))
}
