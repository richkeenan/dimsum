package clients

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

type fakeMDNS struct {
	packets chan mdnsDatagram
	sent    chan []byte
	closed  atomic.Bool
}

func (f *fakeMDNS) Send(_ int, p []byte) error {
	select {
	case f.sent <- p:
	default:
	}
	return nil
}
func (f *fakeMDNS) Close()                       { f.closed.Store(true) }
func (f *fakeMDNS) Packets() <-chan mdnsDatagram { return f.packets }
func (f *fakeMDNS) Interfaces() []int            { return []int{1} }
func TestMDNSManagerEnrichesOverrideAndInvalidatesOldView(t *testing.T) {
	ip := netip.MustParseAddr("192.168.10.20")
	var current atomic.Pointer[View]
	v, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true}}, []Override{{Address: ip.String(), Name: "Owner name"}}, nil)
	require.NoError(t, err)
	current.Store(v)
	m := New(current.Load)
	opened := make(chan *fakeMDNS, 4)
	m.openMDNS = func(context.Context, MDNSSettings) (mdnsTransport, []string) {
		f := &fakeMDNS{packets: make(chan mdnsDatagram, 4), sent: make(chan []byte, 16)}
		opened <- f
		return f, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	var f *fakeMDNS
	select {
	case f = <-opened:
	case <-time.After(time.Second):
		t.Fatal("discovery did not start")
	}
	m.Observe(ip)
	f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t, rr(t, "20.10.168.192.in-addr.arpa. 120 IN PTR Example-iPhone.local."), rr(t, "Example-iPhone.local. 120 IN A 192.168.10.20"))}
	require.Eventually(t, func() bool { n := m.Get(ip); return n.Device != nil && n.Device.Category == "phone" }, 3*time.Second, 10*time.Millisecond)
	assert.Equal(t, "Owner name", m.Get(ip).Name)
	got := m.Get(ip)
	got.Device.Evidence[0].Hostname = "mutation"
	assert.NotEqual(t, "mutation", m.Get(ip).Device.Evidence[0].Hostname)
	next, err := NewView(Settings{}, []Override{{Address: ip.String(), Name: "New owner name"}}, nil)
	require.NoError(t, err)
	current.Store(next)
	assert.Equal(t, "New owner name", m.Get(ip).Name)
	require.Eventually(t, f.closed.Load, time.Second, 10*time.Millisecond)
	assert.Empty(t, m.Get(ip).Device.Evidence)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}
func TestMDNSSettingsValidation(t *testing.T) {
	for _, names := range [][]string{{""}, {"eth0", "eth0"}, {"bad\nname"}} {
		_, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true, Interfaces: names}}, nil, nil)
		assert.Error(t, err)
	}
}
func TestMDNSQueryEncoding(t *testing.T) {
	p := encodeMDNSQuery("_services._dns-sd._udp.local", 12)
	require.NotEmpty(t, p)
	assert.Equal(t, byte(1), p[5])
	assert.Equal(t, []byte{0, 12, 0, 1}, p[len(p)-4:])
}
