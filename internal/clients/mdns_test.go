package clients

import (
	"context"
	"encoding/json"
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

func TestMDNSDiagnosticsDistinguishQueriesFromMalformedResponses(t *testing.T) {
	v, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	m := New(func() *View { return v })
	f := &fakeMDNS{packets: make(chan mdnsDatagram, 4), sent: make(chan []byte, 16)}
	m.openMDNS = func(context.Context, MDNSSettings) (mdnsTransport, []string) { return f, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	f.packets <- mdnsDatagram{iface: 1, wire: encodeMDNSQuery("_services._dns-sd._udp.local", 12)}
	f.packets <- mdnsDatagram{iface: 1, wire: []byte{0, 0, 0x84}}
	f.packets <- mdnsDatagram{iface: 1, wire: bonjourPacket()}
	require.Eventually(t, func() bool { return m.Diagnostics().Responses == 1 }, 3*time.Second, 10*time.Millisecond)
	d := m.Diagnostics()
	assert.Equal(t, uint64(1), d.Dropped, "ordinary multicast queries are not errors")
	wire, err := json.Marshal(d)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(wire, &fields))
	assert.Equal(t, float64(1), fields["ignored_queries"])
	assert.Equal(t, float64(1), fields["malformed_responses"])
	assert.Equal(t, float64(0), fields["send_errors"])
}

func TestMDNSHistoryReadRediscoversWithoutNewDNSQuery(t *testing.T) {
	v, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	m := New(func() *View { return v })
	f := &fakeMDNS{packets: make(chan mdnsDatagram, 1), sent: make(chan []byte, 16)}
	m.openMDNS = func(context.Context, MDNSSettings) (mdnsTransport, []string) { return f, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	ip := netip.MustParseAddr("fd00::20")
	reverse, err := dns.ReverseAddr(ip.String())
	require.NoError(t, err)
	f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t,
		rr(t, reverse+" 120 IN PTR Example-laptop.local."),
		rr(t, "Example-laptop.local. 120 IN AAAA fd00::20"))}
	// The history provider calls Get; no DNS traffic/Observe is needed after boot.
	require.Eventually(t, func() bool { return m.Get(ip).Name == "example-laptop.local" }, 3*time.Second, 10*time.Millisecond)
}

func TestMDNSRefreshesLiveAddressesBeforeExpiryWithoutPolling(t *testing.T) {
	v, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	m := New(func() *View { return v })
	f := &fakeMDNS{packets: make(chan mdnsDatagram, 1), sent: make(chan []byte, 64)}
	m.openMDNS = func(context.Context, MDNSSettings) (mdnsTransport, []string) { return f, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	start := time.Now()
	f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t,
		rr(t, "20.2.0.192.in-addr.arpa. 120 IN PTR Example.local."),
		rr(t, "Example.local. 8 IN A 192.0.2.20"))}
	timeout := time.NewTimer(8 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case wire := <-f.sent:
			var q dns.Msg
			require.NoError(t, q.Unpack(wire))
			if q.Question[0].Name == "example.local." && q.Question[0].Qtype == dns.TypeA {
				assert.GreaterOrEqual(t, time.Since(start), 6*time.Second, "fresh answers should suppress redundant queries")
				return // The timer also proves renewal happened before the 8s TTL.
			}
		case <-timeout.C:
			t.Fatal("address was not refreshed before its TTL expired")
		}
	}
}

func TestMDNSServiceChainSurvivesReverseBacklog(t *testing.T) {
	v, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	m := New(func() *View { return v })
	f := &fakeMDNS{packets: make(chan mdnsDatagram, 4), sent: make(chan []byte, 64)}
	m.openMDNS = func(context.Context, MDNSSettings) (mdnsTransport, []string) { return f, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	for i := 1; i <= 200; i++ {
		m.mdnsObserve <- netip.AddrFrom4([4]byte{192, 0, 2, byte(i)})
	}
	f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t, rr(t, "_services._dns-sd._udp.local. 120 IN PTR _airplay._tcp.local."))}
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case packet := <-f.sent:
			var q dns.Msg
			require.NoError(t, q.Unpack(packet))
			question := q.Question[0]
			switch {
			case question.Name == "_airplay._tcp.local." && question.Qtype == 12:
				f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t, rr(t, "_airplay._tcp.local. 120 IN PTR Example._airplay._tcp.local."))}
			case question.Name == "example._airplay._tcp.local." && question.Qtype == 33:
				f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t, rr(t, "Example._airplay._tcp.local. 120 IN SRV 0 0 7000 Example-TV.local."))}
			case question.Name == "example-tv.local." && question.Qtype == 1:
				f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t, rr(t, "Example-TV.local. 120 IN A 192.0.2.20"))}
				require.Eventually(t, func() bool { return m.Get(netip.MustParseAddr("192.0.2.20")).Name == "Example" }, 2*time.Second, 10*time.Millisecond)
				assert.Equal(t, "example-tv.local", m.Get(netip.MustParseAddr("192.0.2.20")).Device.Hostname)
				return
			}
		case <-timeout.C:
			t.Fatal("reverse backlog starved the service-to-address discovery chain")
		}
	}
}

func TestMDNSShortenedTTLAdvancesPendingRefresh(t *testing.T) {
	v, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	m := New(func() *View { return v })
	f := &fakeMDNS{packets: make(chan mdnsDatagram, 4), sent: make(chan []byte, 64)}
	m.openMDNS = func(context.Context, MDNSSettings) (mdnsTransport, []string) { return f, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t,
		rr(t, "20.2.0.192.in-addr.arpa. 120 IN PTR Example.local."),
		rr(t, "Example.local. 120 IN A 192.0.2.20"))}
	// Allow the initial enumeration, A and AAAA scheduling decisions to finish.
	setup := time.NewTimer(2 * time.Second)
	defer setup.Stop()
	<-setup.C
	f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t, rr(t, "Example.local. 6 IN A 192.0.2.20"))}
	timeout := time.NewTimer(6 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case packet := <-f.sent:
			var q dns.Msg
			require.NoError(t, q.Unpack(packet))
			if q.Question[0].Name == "example.local." && q.Question[0].Qtype == 1 {
				return
			}
		case <-timeout.C:
			t.Fatal("shortened TTL did not advance the previously scheduled renewal")
		}
	}
}

func TestMDNSLateAnswerRenewsDuringRetryCooldown(t *testing.T) {
	v, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	m := New(func() *View { return v })
	f := &fakeMDNS{packets: make(chan mdnsDatagram, 4), sent: make(chan []byte, 64)}
	m.openMDNS = func(context.Context, MDNSSettings) (mdnsTransport, []string) { return f, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t, rr(t, "20.2.0.192.in-addr.arpa. 120 IN PTR Example.local."))}
	timeout := time.NewTimer(12 * time.Second)
	defer timeout.Stop()
	attempts := 0
	for {
		select {
		case packet := <-f.sent:
			var q dns.Msg
			require.NoError(t, q.Unpack(packet))
			if q.Question[0].Name != "example.local." || q.Question[0].Qtype != 1 {
				continue
			}
			attempts++
			if attempts == 3 {
				f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t, rr(t, "Example.local. 4 IN A 192.0.2.20"))}
				timeout.Reset(4 * time.Second)
			} else if attempts == 4 {
				return
			}
		case <-timeout.C:
			t.Fatalf("late answer did not renew before expiry; attempts=%d", attempts)
		}
	}
}
