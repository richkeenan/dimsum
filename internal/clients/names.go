// Package clients maintains derived friendly names keyed by observed IP, never by name.
package clients

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type Settings struct {
	Resolver  string `yaml:"resolver,omitempty"`
	HostsFile string `yaml:"hosts_file,omitempty"`
}
type Override struct {
	Address string `yaml:"address"`
	Name    string `yaml:"name"`
}
type View struct {
	settings         Settings
	overrides, local map[netip.Addr]string
}

func NewView(settings Settings, overrides []Override, local map[netip.Addr]string) (*View, error) {
	if settings.Resolver != "" {
		a, e := netip.ParseAddrPort(settings.Resolver)
		if e != nil || a.Port() == 0 || !localAddress(a.Addr()) {
			return nil, fmt.Errorf("naming resolver must be an explicit local unicast IP:port")
		}
	}
	v := &View{settings: settings, overrides: map[netip.Addr]string{}, local: map[netip.Addr]string{}}
	for _, o := range overrides {
		a, e := netip.ParseAddr(o.Address)
		if e != nil || strings.TrimSpace(o.Name) == "" || v.overrides[a.Unmap()] != "" {
			return nil, fmt.Errorf("invalid or duplicate client override")
		}
		v.overrides[a.Unmap()] = o.Name
	}
	for a, n := range local {
		v.local[a.Unmap()] = n
	}
	return v, nil
}
func localAddress(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsValid() && !a.IsUnspecified() && !a.IsMulticast() && (a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast())
}

type Name struct {
	Address  netip.Addr `json:"address"`
	Name     string     `json:"name,omitempty"`
	Source   string     `json:"source"`
	Updated  time.Time  `json:"updated,omitempty"`
	Expires  time.Time  `json:"expires,omitempty"`
	Fresh    bool       `json:"fresh"`
	Negative bool       `json:"negative"`
	Error    string     `json:"error,omitempty"`
}
type entry struct {
	view *View
	name Name
}
type job struct {
	view    *View
	address netip.Addr
}

// Manager has one bounded worker and at most 4096 cached identities. Observe and
// Get never perform file/network IO. Run is called once by the service lifecycle.
type Manager struct {
	current func() *View
	mu      sync.Mutex
	cache   map[netip.Addr]entry
	pending map[job]bool
	queue   chan job
}

func New(current func() *View) *Manager {
	return &Manager{current: current, cache: map[netip.Addr]entry{}, pending: map[job]bool{}, queue: make(chan job, 128)}
}
func (m *Manager) Get(address netip.Addr) Name { return m.get(address.Unmap(), m.current()) }
func (m *Manager) get(a netip.Addr, v *View) Name {
	n := Name{Address: a, Source: "unknown"}
	if v == nil {
		return n
	}
	if name := v.overrides[a]; name != "" {
		n.Name = name
		n.Source = "override"
		n.Fresh = true
		return n
	}
	if name := v.local[a]; name != "" {
		n.Name = name
		n.Source = "local"
		n.Fresh = true
		return n
	}
	m.mu.Lock()
	e, ok := m.cache[a]
	m.mu.Unlock()
	if ok && e.view == v {
		n = e.name
		n.Fresh = time.Now().Before(n.Expires)
	}
	return n
}
func (m *Manager) Observe(address netip.Addr) {
	a := address.Unmap()
	if !localAddress(a) {
		return
	}
	v := m.current()
	if v == nil || m.get(a, v).Fresh {
		return
	}
	j := job{v, a}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending[j] {
		return
	}
	select {
	case m.queue <- j:
		m.pending[j] = true
	default:
	}
}
func (m *Manager) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-m.queue:
			if j.view != m.current() {
				m.mu.Lock()
				delete(m.pending, j)
				m.mu.Unlock()
				continue
			}
			lookupCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
			n := discover(lookupCtx, j.view, j.address)
			cancel()
			m.mu.Lock()
			delete(m.pending, j)
			if len(m.cache) >= 4096 {
				for a := range m.cache {
					delete(m.cache, a)
					break
				}
			}
			if ctx.Err() == nil && j.view == m.current() {
				m.cache[j.address] = entry{j.view, n}
			}
			m.mu.Unlock()
		}
	}
}
