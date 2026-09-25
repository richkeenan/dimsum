package app

import (
	"context"
	"encoding/binary"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dhcp"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/localdns"
	"github.com/richkeenan/dimsum/internal/resolve"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDHCPPublicationGenerationAndDisable(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, _, err := dhcp.OpenLeaseStore(filepath.Join(dir, "dhcp"), 1024, nil)
	require.NoError(t, err)
	mac := [6]byte{2, 0, 0, 0, 0, 1}
	ip := netip.MustParseAddr("192.0.2.100")
	lease := dhcp.Lease{Identity: "mac:" + string(mac[:]), MAC: mac, Address: ip, Hostname: "desk", Expiry: time.Now().Add(time.Hour), HoldUntil: time.Now().Add(2 * time.Hour), State: dhcp.Bound}
	require.NoError(t, db.Submit(ctx, dhcp.Mutation{Token: dhcp.Token{Generation: 1, Sequence: 1}, Kind: dhcp.PutLease, Lease: lease}))
	select {
	case result, ok := <-db.Results():
		require.True(t, ok)
		require.NoError(t, result.Err)
	case <-time.After(time.Second):
		t.Fatal("commit timeout")
	}
	require.NoError(t, db.Close(ctx))
	supervisor := NewDHCPSupervisor(dir, []string{"0.0.0.0:53"})
	link := &appDHCPPacketLink{appDHCPLink: appDHCPLink{done: make(chan struct{})}, packets: make(chan []byte, 1)}
	opens := 0
	supervisor.openLink = func(s dhcp.Settings) (dhcp.Link, dhcp.ProbeFunc, error) {
		opens++
		if opens > 1 {
			return fixtureDHCPLink(s)
		}
		return link, func(context.Context, netip.Addr) (bool, error) { return false, nil }, nil
	}
	t.Cleanup(func() { require.NoError(t, supervisor.Close(ctx)) })
	text := `version: 1
dns:
  listen: [0.0.0.0:53]
admin:
  listen: 127.0.0.1:0
paths:
  data_dir: data
  secrets_dir: secrets
dhcp:
  enabled: true
  interface: fixture0
  server_ip: 192.0.2.2
  subnet: 192.0.2.0/24
  gateway: 192.0.2.1
  range_start: 192.0.2.100
  range_end: 192.0.2.110
  lease_seconds: 3600
  local_domain: home.arpa
records:
  - {name: alias.test, type: CNAME, value: desk.home.arpa, ttl: 60}
clients:
  - id: desk
    icon: bell
    selectors:
      macs: ["02:00:00:00:00:01"]
`
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	store, err := config.OpenStore(ctx, path, path+".state", config.StoreOptions{Offline: true})
	require.NoError(t, err)
	publication := newDHCPNames(supervisor.View)
	snap := store.Snapshot()
	require.Nil(t, publication.capture(snap))
	require.NoError(t, supervisor.Reconcile(ctx, snap.Config().DHCP, snap.Generation()))
	v := publication.capture(snap)
	require.NotNil(t, v)
	assert.Same(t, v, publication.capture(snap))
	assert.Equal(t, "desk.home.arpa", v.Name(ip, time.Now()).Hostname)
	assert.Equal(t, "02:00:00:00:00:01", v.AuthoritativeMAC(ip, time.Now()))
	assert.Empty(t, v.AuthoritativeMAC(ip, lease.Expiry))
	var captured *localdns.Leases
	allocs := testing.AllocsPerRun(100, func() { captured = publication.capture(snap) })
	assert.Same(t, v, captured)
	assert.Zero(t, allocs)
	names := clients.New(func() *clients.View { return store.Snapshot().Names() })
	names.SetDHCP(func(view *clients.View, a netip.Addr) (clients.Name, bool) {
		return publication.name(store.Snapshot(), view, a)
	})
	names.SetIcon(func(view *clients.View, a netip.Addr) string {
		return publication.icon(store.Snapshot(), view, a)
	})
	assert.Equal(t, "desk.home.arpa", names.Get(ip).Name)
	assert.Equal(t, "bell", names.Get(ip).Device.Icon)
	p := resolve.NewWithStore(nil, store)
	p.SetLeases(publication.capture)
	t.Cleanup(func() { require.NoError(t, p.Close()) })
	q := new(dns.Msg)
	q.SetQuestion("alias.test.", dns.TypeA)
	wire, err := q.Pack()
	require.NoError(t, err)
	r := transport.Request{Wire: wire}
	require.NoError(t, dnswire.ParseRequest(wire, &r.Message))
	out := make([]byte, 1232)
	n, err := p.Resolve(ctx, &r, out)
	require.NoError(t, err)
	var got dns.Msg
	require.NoError(t, got.Unpack(out[:n]))
	require.Len(t, got.Answer, 2)
	assert.Equal(t, "192.0.2.100", got.Answer[1].(*dns.A).A.String())
	// A real committed renewal changes only the lease publication, not config,
	// filtering identity, or the pipeline's existing upstream cache counters.
	policy := snap.Policy()
	cacheStats := p.CacheStats()
	originalSource := supervisor.View()
	request := make([]byte, 244)
	request[0] = 1
	request[1] = 1
	request[2] = 6
	binary.BigEndian.PutUint32(request[4:8], 42)
	copy(request[12:16], ip.AsSlice())
	copy(request[28:34], mac[:])
	copy(request[236:], []byte{99, 130, 83, 99, 53, 1, 3, 255})
	link.packets <- request
	require.Eventually(t, func() bool { return supervisor.View() != originalSource }, time.Second, time.Millisecond)
	renewed := publication.capture(snap)
	require.NotNil(t, renewed)
	assert.NotSame(t, v, renewed)
	assert.Same(t, snap, store.Snapshot())
	assert.Same(t, policy, store.Snapshot().Policy())
	assert.Equal(t, cacheStats, p.CacheStats())
	assert.Equal(t, "desk.home.arpa", renewed.Name(ip, time.Now()).Hostname)
	// Model a publication advancing between the cold capture and its build lock.
	// Re-read there, rather than letting a delayed reader republish retired data.
	reads := 0
	catchingUp := newDHCPNames(func() *dhcp.LeaseView {
		reads++
		if reads == 1 {
			return originalSource
		}
		return supervisor.View()
	})
	caughtUp := catchingUp.capture(snap)
	require.NotNil(t, caughtUp)
	assert.Equal(t, supervisor.View().Lease(0).Expiry, caughtUp.Name(ip, time.Now()).Expiry)
	// Reload without reconciling DHCP: old applied data must immediately disappear.
	text = strings.Replace(text, "ttl: 60", "ttl: 61", 1)
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	_, err = store.Reload(ctx)
	require.NoError(t, err)
	next := store.Snapshot()
	require.NotEqual(t, snap.Generation(), next.Generation())
	assert.Nil(t, publication.capture(next))
	assert.Empty(t, names.Get(ip).Name)
	assert.Empty(t, names.Get(ip).Device.Icon, "retired leases must not select configured icons")
	require.NoError(t, supervisor.Reconcile(ctx, next.Config().DHCP, next.Generation()))
	assert.NotNil(t, publication.capture(next))
	assert.Equal(t, "desk.home.arpa", names.Get(ip).Name)
	staleName, _ := publication.name(next, snap.Names(), ip)
	assert.Empty(t, staleName.Name)
	assert.Empty(t, publication.icon(next, snap.Names(), ip))
	assert.Equal(t, "bell", names.Get(ip).Device.Icon)
	// Concurrent captures during publication switches exercise actual runtime views.
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				s := store.Snapshot()
				v := publication.capture(s)
				if v != nil && v.Generation() != s.Generation() {
					t.Error("mixed generations")
				}
			}
		}()
	}
	text = strings.Replace(text, "enabled: true", "enabled: false", 1)
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	_, err = store.Reload(ctx)
	require.NoError(t, err)
	disabled := store.Snapshot()
	assert.Nil(t, publication.capture(disabled))
	assert.Empty(t, names.Get(ip).Name)
	assert.Empty(t, names.Get(ip).Device.Icon)
	require.NoError(t, supervisor.Reconcile(ctx, disabled.Config().DHCP, disabled.Generation()))
	wg.Wait()
	text = strings.Replace(text, "enabled: false", "enabled: true", 1)
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	_, err = store.Reload(ctx)
	require.NoError(t, err)
	enabled := store.Snapshot()
	require.NoError(t, supervisor.Reconcile(ctx, enabled.Config().DHCP, enabled.Generation()))
	assert.Equal(t, "desk.home.arpa", names.Get(ip).Name)
}
