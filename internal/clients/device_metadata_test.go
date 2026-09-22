package clients

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/netip"
	"testing"
	"time"
)

func TestThreadVendorModelIdentification(t *testing.T) {
	now := time.Now()
	for _, service := range []string{"_meshcop._udp", "_other._tcp"} {
		t.Run(service, func(t *testing.T) {
			c := newMDNSCache()
			require.NoError(t, c.ingest(2, mdnsPacket(t,
				rr(t, fmt.Sprintf("%s.local. 120 IN PTR amazon.%s.local.", service, service)),
				rr(t, fmt.Sprintf("amazon.%s.local. 120 IN SRV 0 0 49154 linux.local.", service)),
				rr(t, fmt.Sprintf("amazon.%s.local. 1 IN TXT \"vn=amazon\" \"mn=echo\"", service)),
				rr(t, "linux.local. 120 IN A 192.0.2.20")), now))
			n := enrichDiscovered(c.lookup(netip.MustParseAddr("192.0.2.20"), now), now)
			require.NotNil(t, n.Device)
			if service == "_meshcop._udp" {
				assert.Equal(t, "Amazon Echo", n.Name)
				assert.Equal(t, "Amazon", n.Device.Manufacturer)
				assert.Equal(t, "Echo", n.Device.Model)
				assert.Equal(t, "speaker", n.Device.Category)
				assert.False(t, n.Device.Inferred)
			} else {
				assert.Equal(t, "linux.local", n.Name)
				assert.Empty(t, n.Device.Model)
			}
			later := mergeDiscovered(Name{Source: "unknown"}, n, now.Add(2*time.Second))
			assert.Equal(t, "linux.local", later.Name)
			assert.Empty(t, later.Device.Model)
			assert.Equal(t, "unknown", later.Device.Category)
		})
	}
}
