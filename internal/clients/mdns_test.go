package clients

import (
	"context"
	"errors"
	"github.com/miekg/dns"
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
	ip := netip.MustParseAddr("192.0.2.20")
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
	// Inject documentation addresses directly; production Observe excludes them.
	m.mdnsObserve <- ip
	f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t, rr(t, "20.2.0.192.in-addr.arpa. 120 IN PTR Example-iPhone.local."), rr(t, "Example-iPhone.local. 120 IN A 192.0.2.20"))}
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
	// Old socket packets cannot repopulate evidence after a naming-view change.
	f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t, rr(t, "20.2.0.192.in-addr.arpa. 120 IN PTR Obsolete.local."), rr(t, "Obsolete.local. 120 IN A 192.0.2.20"))}
	removed, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true, Interfaces: []string{"eth1"}}}, nil, nil)
	require.NoError(t, err)
	current.Store(removed)
	assert.Empty(t, m.Get(ip).Name)
	var replacement *fakeMDNS
	select {
	case replacement = <-opened:
	case <-time.After(time.Second):
		t.Fatal("replacement transport did not open")
	}
	assert.Empty(t, m.Get(ip).Device.Evidence)
	replacement.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t, rr(t, "20.2.0.192.in-addr.arpa. 120 IN PTR Current.local."), rr(t, "Current.local. 120 IN A 192.0.2.20"))}
	require.Eventually(t, func() bool { return m.Get(ip).Name == "current.local" }, 3*time.Second, 10*time.Millisecond)
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

func TestMDNSTransportFailureClosesSocketsAndReportsUnavailable(t *testing.T) {
	v, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	m := New(func() *View { return v })
	f := &fakeMDNS{packets: make(chan mdnsDatagram, 1), sent: make(chan []byte, 16)}
	m.openMDNS = func(context.Context, MDNSSettings) (mdnsTransport, []string) { return f, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	require.Eventually(t, func() bool { return m.Diagnostics().Running }, time.Second, time.Millisecond)
	f.packets <- mdnsDatagram{err: errors.New("test receive failure")}
	require.Eventually(t, func() bool { return !m.Diagnostics().Running && f.closed.Load() }, time.Second, time.Millisecond)
	assert.Contains(t, m.Diagnostics().Errors, "test receive failure")
}
func TestMDNSQueryEncoding(t *testing.T) {
	p := encodeMDNSQuery("_services._dns-sd._udp.local", 12)
	require.NotEmpty(t, p)
	assert.Equal(t, byte(1), p[5])
	assert.Equal(t, []byte{0, 12, 0, 1}, p[len(p)-4:])
}

func TestMDNSQueryPreservesServiceLabelBytes(t *testing.T) {
	for _, name := range []string{`example\032television._airplay._tcp.local`, `example\046printer._ipp._tcp.local`, `caf\195\169._http._tcp.local`} {
		packet := encodeMDNSQuery(name, 33)
		require.NotEmpty(t, packet, name)
		var q dns.Msg
		require.NoError(t, q.Unpack(packet))
		want := new(dns.Msg)
		want.SetQuestion(name+".", 33)
		want.Id = 0
		want.RecursionDesired = false
		encoded, err := want.Pack()
		require.NoError(t, err)
		assert.Equal(t, encoded, packet)
	}
	for _, name := range []string{`bad\999.local`, `bad\03.local`, `.local`, `bad..local`} {
		assert.Empty(t, encodeMDNSQuery(name, 12))
	}
}

func TestMDNSEnumerationSurvivesQueuedReverseQueries(t *testing.T) {
	v, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	m := New(func() *View { return v })
	f := &fakeMDNS{packets: make(chan mdnsDatagram), sent: make(chan []byte, 16)}
	m.openMDNS = func(context.Context, MDNSSettings) (mdnsTransport, []string) { return f, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	m.mdnsObserve <- netip.MustParseAddr("192.0.2.21")
	m.mdnsObserve <- netip.MustParseAddr("192.0.2.22")
	timeout := time.NewTimer(4 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case p := <-f.sent:
			var q dns.Msg
			require.NoError(t, q.Unpack(p))
			if len(q.Question) > 0 && q.Question[0].Name == "_services._dns-sd._udp.local." {
				return
			}
		case <-timeout.C:
			t.Fatal("service enumeration was lost behind client queries")
		}
	}
}
