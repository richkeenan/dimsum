package clients

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type durableNames interface {
	NameSnapshot(time.Time) (string, []Name)
	RestoreNames(string, []Name, time.Time)
}

func TestRestoredDiscoverySurvivesWorkerStartupAndKeepsDeadline(t *testing.T) {
	now := time.Now()
	address := netip.MustParseAddr("192.0.2.20")
	view, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	before := New(func() *View { return view })
	before.mdnsNames[address] = entry{view, Name{Address: address, Name: "Work laptop", Source: "dns-sd", Updated: now.Add(-47 * time.Hour), Expires: now.Add(-46 * time.Hour),
		Device: &Enrichment{Hostname: "work-macbook.local", Category: "laptop", Evidence: []Evidence{}}}}
	saved, ok := any(before).(durableNames)
	require.True(t, ok, "the manager must support durable discovery snapshots")
	scope, rows := saved.NameSnapshot(now)
	require.Len(t, rows, 1)
	// A new publication pointer must not invalidate compatible persisted evidence.
	restartedView, err := NewView(view.settings, nil, nil)
	require.NoError(t, err)
	after := New(func() *View { return restartedView })
	restored := any(after).(durableNames)
	restored.RestoreNames(scope, rows, now)
	rows[0].Device.Category = "phone"
	after.ReplaceDNSActivity([]DNSActivity{{Address: address, Domain: "example-ats.iot.us-west-2.amazonaws.com", First: now.Add(-time.Minute), Last: now, Corroborated: now.Add(-time.Minute), Count: 2}})
	first := after.Get(address)
	assert.Equal(t, "Work laptop", first.Name)
	assert.Equal(t, "laptop", first.Device.Category)
	assert.Equal(t, now.Add(-47*time.Hour), first.Updated)
	assert.False(t, first.Fresh)
	f := &fakeMDNS{packets: make(chan mdnsDatagram, 1), sent: make(chan []byte, 16)}
	after.openMDNS = func(context.Context, MDNSSettings) (mdnsTransport, []string) { return f, nil }
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); after.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	require.Eventually(t, func() bool { return after.Diagnostics().Running }, time.Second, time.Millisecond)
	assert.Equal(t, "Work laptop", after.Get(address).Name, "starting discovery must not clear restored names")
	// Restored clients are discovery interests without new DNS traffic or UI reads.
	f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t,
		rr(t, "20.2.0.192.in-addr.arpa. 120 IN PTR Replacement.local."),
		rr(t, "Replacement.local. 120 IN A 192.0.2.20"))}
	require.Eventually(t, func() bool { return after.Get(address).Name == "replacement.local" }, 3*time.Second, 10*time.Millisecond)
	_, expired := saved.NameSnapshot(now.Add(time.Hour))
	assert.Empty(t, expired, "persisting or viewing must not extend the original 48 hours")
}

func TestNameSnapshotsRespectSourcesAndSettings(t *testing.T) {
	now := time.Now()
	var current atomic.Pointer[View]
	v, err := NewView(Settings{Resolver: "127.0.0.1:5354", MDNS: MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	current.Store(v)
	m := New(current.Load)
	durable, ok := any(m).(durableNames)
	require.True(t, ok)
	scope, _ := durable.NameSnapshot(now)
	address := netip.MustParseAddr("192.0.2.20")
	base := Name{Address: address, Name: "Example laptop", Source: "mdns", Updated: now.Add(-time.Hour), Expires: now.Add(-time.Minute)}
	durable.RestoreNames(scope, []Name{base}, now)
	assert.Equal(t, "Example laptop", m.Get(address).Name)
	// Naming overrides should take priority without discarding the discovered identity.
	v2, err := NewView(v.settings, []Override{{address.String(), "Owner label"}}, nil)
	require.NoError(t, err)
	current.Store(v2)
	assert.Equal(t, "Owner label", m.Get(address).Name)
	nextScope, rows := durable.NameSnapshot(now)
	assert.Equal(t, scope, nextScope)
	require.Len(t, rows, 1)
	assert.Equal(t, "Example laptop", rows[0].Name)
	changed, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true, Interfaces: []string{"fixture0"}}}, nil, nil)
	require.NoError(t, err)
	current.Store(changed)
	durable.RestoreNames(scope, rows, now)
	assert.Empty(t, m.Get(address).Name)
	current.Store(v)
	for _, source := range []string{"override", "local", "dhcp", "dns-guess", "unknown"} {
		n := base
		n.Source = source
		fresh := New(current.Load)
		any(fresh).(durableNames).RestoreNames(scope, []Name{n}, now)
		assert.Empty(t, fresh.Get(address).Name, source)
	}
	for _, source := range []string{"hosts", "router-ptr"} {
		fresh := New(current.Load)
		n := base
		n.Source, n.Expires = source, now.Add(time.Minute)
		any(fresh).(durableNames).RestoreNames(scope, []Name{n}, now)
		assert.Equal(t, "Example laptop", fresh.Get(address).Name)
		_, rows := any(fresh).(durableNames).NameSnapshot(now.Add(time.Minute))
		assert.Empty(t, rows, "resolved names retain their own TTL")
	}
}

func TestRestoredNamesAreBoundedAndDoNotReviveExpiredIdentities(t *testing.T) {
	now := time.Now()
	v, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	m := New(func() *View { return v })
	scope, _ := m.NameSnapshot(now)
	address := netip.MustParseAddr("2001:db8::1")
	rows := make([]Name, 4097)
	for i := range rows {
		rows[i] = Name{Address: address, Name: "Example laptop", Source: "mdns", Updated: now.Add(-time.Hour), Expires: now.Add(-time.Minute)}
		address = address.Next()
	}
	m.RestoreNames(scope, rows, now)
	_, saved := m.NameSnapshot(now)
	assert.Len(t, saved, 4096)
	expired := New(func() *View { return v })
	expired.RestoreNames(scope, rows, now.Add(47*time.Hour))
	assert.Empty(t, expired.Get(rows[0].Address).Name)
	assert.Empty(t, expired.mdnsNames)
}

func TestNamingScopeTreatsEmptyInterfaceListsEqually(t *testing.T) {
	a, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	b, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true, Interfaces: []string{}}}, nil, nil)
	require.NoError(t, err)
	assert.True(t, compatibleNames(a, b), "equivalent settings must not discard remembered names")
}
