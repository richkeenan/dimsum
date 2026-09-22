package clients_test

import (
	"context"
	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHistoryReadDiscoversConfiguredHostsWithoutDNSQueries(t *testing.T) {
	file := filepath.Join(t.TempDir(), "hosts")
	require.NoError(t, os.WriteFile(file, []byte("fd00::20 example-laptop\n"), 0600))
	v, err := clients.NewView(clients.Settings{HostsFile: file}, nil, nil)
	require.NoError(t, err)
	m := clients.New(func() *clients.View { return v })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	require.Eventually(t, func() bool { return m.Get(netip.MustParseAddr("fd00::20")).Name == "example-laptop" }, time.Second, time.Millisecond)
}

func TestAsyncNamesPrecedenceFreshnessAndIdentity(t *testing.T) {
	ip := netip.MustParseAddr("192.168.1.2")
	v6 := netip.MustParseAddr("fd00::2")
	file := filepath.Join(t.TempDir(), "hosts")
	require.NoError(t, os.WriteFile(file, []byte("192.168.1.2 hosts-name\nfd00::2 hosts-name\n"), 0600))
	view, err := clients.NewView(clients.Settings{HostsFile: file}, nil, nil)
	require.NoError(t, err)
	var active atomic.Pointer[clients.View]
	active.Store(view)
	m := clients.New(func() *clients.View { return active.Load() })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	m.Observe(ip)
	m.Observe(v6)
	require.Eventually(t, func() bool { return m.Get(ip).Source == "hosts" && m.Get(v6).Source == "hosts" }, time.Second, time.Millisecond)
	a, b := m.Get(ip), m.Get(v6)
	assert.Equal(t, a.Name, b.Name)
	assert.NotEqual(t, a.Address, b.Address)
	assert.True(t, a.Fresh)
	assert.False(t, a.Negative)
	next, err := clients.NewView(clients.Settings{HostsFile: file}, []clients.Override{{Address: ip.String(), Name: "owner"}}, map[netip.Addr]string{ip: "local", v6: "local-v6"})
	require.NoError(t, err)
	active.Store(next)
	assert.Equal(t, "override", m.Get(ip).Source)
	assert.Equal(t, "owner", m.Get(ip).Name)
	assert.Equal(t, "local", m.Get(v6).Source)
	unknown := netip.MustParseAddr("192.168.1.99")
	m.Observe(unknown)
	require.Eventually(t, func() bool { return m.Get(unknown).Negative }, time.Second, time.Millisecond)
	assert.True(t, m.Get(unknown).Fresh)
	updated := m.Get(unknown).Updated
	m.Observe(unknown)
	assert.Equal(t, updated, m.Get(unknown).Updated)
}

func TestRouterPTRIsAsyncAndNegativeCached(t *testing.T) {
	workerErrors := make(chan error, 8)
	gate := make(chan struct{})
	started := make(chan struct{}, 8)
	var unblock sync.Once
	u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		started <- struct{}{}
		<-gate
		var q dns.Msg
		if e := q.Unpack(r.Wire); e != nil {
			workerErrors <- e
			return testutil.Response{Drop: true}
		}
		m := new(dns.Msg)
		m.SetReply(&q)
		if q.Question[0].Name == "2.1.168.192.in-addr.arpa." {
			m.Answer = []dns.RR{&dns.PTR{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: 12, Class: 1, Ttl: 60}, Ptr: "router-name.home.arpa."}}
		} else {
			m.Rcode = dns.RcodeNameError
		}
		b, e := m.Pack()
		if e != nil {
			workerErrors <- e
		}
		return testutil.Response{Wire: b}
	})
	require.NoError(t, err)
	defer u.Close()
	defer unblock.Do(func() { close(gate) })
	view, err := clients.NewView(clients.Settings{Resolver: u.Address()}, nil, nil)
	require.NoError(t, err)
	m := clients.New(func() *clients.View { return view })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	ip := netip.MustParseAddr("192.168.1.2")
	m.Observe(ip)
	select {
	case <-started:
	case <-time.After(time.Second):
		require.FailNow(t, "PTR did not start")
	}
	assert.Equal(t, "unknown", m.Get(ip).Source, "lookup has not completed")
	for range 100 {
		m.Observe(ip)
	}
	unblock.Do(func() { close(gate) })
	require.Eventually(t, func() bool { return m.Get(ip).Source == "router-ptr" }, time.Second, time.Millisecond)
	assert.Equal(t, "router-name.home.arpa", m.Get(ip).Name)
	<-u.Requests()
	missing := netip.MustParseAddr("fd00::3")
	m.Observe(missing)
	require.Eventually(t, func() bool { return m.Get(missing).Negative }, time.Second, time.Millisecond)
	assert.Equal(t, "router-ptr", m.Get(missing).Source)
	select {
	case request := <-u.Requests():
		var q dns.Msg
		require.NoError(t, q.Unpack(request.Wire))
		assert.Contains(t, q.Question[0].Name, "ip6.arpa.")
	case <-time.After(time.Second):
		require.FailNow(t, "IPv6 PTR missing")
	}
	for range 100 {
		m.Observe(missing)
	}
	select {
	case <-u.Requests():
		assert.Fail(t, "cached negative retried")
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case e := <-workerErrors:
		assert.NoError(t, e)
	default:
	}
}

func TestNamingRejectsPublicResolver(t *testing.T) {
	_, err := clients.NewView(clients.Settings{Resolver: "1.1.1.1:53"}, nil, nil)
	assert.Error(t, err)
}
