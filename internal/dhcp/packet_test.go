package dhcp

import (
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net"
	"testing"
)

func TestDecodeRequestOwnsAndValidatesOptions(t *testing.T) {
	p, err := dhcpv4.NewDiscovery(net.HardwareAddr{2, 0, 0, 0, 0, 1})
	require.NoError(t, err)
	p.UpdateOption(dhcpv4.OptClientIdentifier([]byte{1, 2, 3}))
	p.UpdateOption(dhcpv4.OptHostName("client"))
	r, err := DecodeRequest(p)
	require.NoError(t, err)
	assert.Equal(t, Discover, r.Type)
	assert.Equal(t, string([]byte{1, 2, 3}), r.ClientID)
	p.Options[dhcpv4.OptionClientIdentifier.Code()][0] = 9
	assert.Equal(t, byte(1), r.ClientID[0])
	p.UpdateOption(dhcpv4.OptClientIdentifier(make([]byte, 256)))
	_, err = DecodeRequest(p)
	assert.Error(t, err)
	p.UpdateOption(dhcpv4.OptClientIdentifier([]byte{1}))
	p.Options[dhcpv4.OptionRequestedIPAddress.Code()] = []byte{1, 2, 3}
	_, err = DecodeRequest(p)
	assert.Error(t, err)
}

func FuzzDecodeRequest(f *testing.F) {
	p, _ := dhcpv4.NewDiscovery(net.HardwareAddr{2, 0, 0, 0, 0, 1})
	f.Add(p.ToBytes())
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 4096 {
			return
		}
		p, err := dhcpv4.FromBytes(b)
		if err != nil {
			return
		}
		r, err := DecodeRequest(p)
		if err == nil {
			assert.LessOrEqual(t, len(r.ClientID), 255)
			assert.LessOrEqual(t, len(r.Hostname), 63)
			assert.True(t, validRequest(r))
		}
	})
}
