package app

import (
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dhcp"
	"github.com/richkeenan/dimsum/internal/localdns"
)

type dhcpNamePublication struct {
	source   *dhcp.LeaseView
	snapshot *config.Snapshot
	leases   *localdns.Leases
}
type dhcpNames struct {
	source  func() *dhcp.LeaseView
	mu      sync.Mutex
	current atomic.Pointer[dhcpNamePublication]
}

func newDHCPNames(source func() *dhcp.LeaseView) *dhcpNames { return &dhcpNames{source: source} }

// capture derives one immutable index per committed source-view identity, shared
// by DNS and display naming. Steady-state capture neither copies lease rows nor
// compiles policy. No job retains the publication; the cache owns only one pair.
func (d *dhcpNames) capture(snapshot *config.Snapshot) *localdns.Leases {
	if snapshot != nil && !snapshot.DHCPEnabled() {
		if d.current.Load() != nil {
			d.current.Store(nil)
		}
		return nil
	}
	source := d.source()
	if source == nil || snapshot != nil && source.Generation() != snapshot.Generation() {
		if d.current.Load() != nil {
			d.current.Store(nil)
		}
		return nil
	}
	if cached := d.current.Load(); cached != nil && cached.source == source && cached.snapshot == snapshot {
		return cached.leases
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	// A committed batch may have advanced while this reader waited. Never
	// replace a newer cached index with that reader's retired source pointer.
	source = d.source()
	if source == nil || snapshot != nil && source.Generation() != snapshot.Generation() {
		d.current.Store(nil)
		return nil
	}
	if cached := d.current.Load(); cached != nil && cached.source == source && cached.snapshot == snapshot {
		return cached.leases
	}
	settings := source.Settings()
	rows := make([]localdns.Lease, 0, source.Len())
	for i := 0; i < source.Len(); i++ {
		l := source.Lease(i)
		if l.State == dhcp.Bound {
			rows = append(rows, localdns.Lease{Address: l.Address, MAC: net.HardwareAddr(l.MAC[:]).String(), Hostname: l.Hostname, Expiry: l.Expiry})
		}
	}
	if cached := d.current.Load(); cached != nil && cached.snapshot == snapshot && cached.source.Generation() == source.Generation() {
		if leases := cached.leases.RefreshExpiries(rows); leases != nil {
			d.current.Store(&dhcpNamePublication{source: source, snapshot: snapshot, leases: leases})
			return leases
		}
	}
	reservations := make(map[netip.Addr]string, len(settings.Reservations))
	for _, r := range settings.Reservations {
		if r.Hostname != "" {
			reservations[netip.MustParseAddr(r.Address)] = r.Hostname
		}
	}
	var explicit *localdns.Zones
	if snapshot != nil {
		explicit = snapshot.Local()
	}
	leases := localdns.BuildLeases(source.Generation(), settings.LocalDomain, rows, reservations, explicit)
	d.current.Store(&dhcpNamePublication{source: source, snapshot: snapshot, leases: leases})
	return leases
}

func (d *dhcpNames) name(snapshot *config.Snapshot, view *clients.View, a netip.Addr) (clients.Name, bool) {
	if snapshot == nil || snapshot.Names() != view {
		return clients.Name{}, false
	}
	lease := d.capture(snapshot).Name(a, time.Now())
	if lease.Hostname == "" {
		return clients.Name{}, false
	}
	source := "dhcp"
	if lease.Reservation {
		source = "local"
	}
	return clients.Name{Address: a, Name: lease.Hostname, Source: source, Expires: lease.Expiry, Fresh: true}, lease.Generated
}

func (d *dhcpNames) icon(snapshot *config.Snapshot, view *clients.View, a netip.Addr) string {
	if snapshot == nil || snapshot.Names() != view {
		return ""
	}
	mac := d.capture(snapshot).AuthoritativeMAC(a, time.Now())
	return snapshot.ClientPolicies().Select(a, mac).Icon()
}
