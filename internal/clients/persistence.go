package clients

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"slices"
	"time"
)

// The scope binds disposable observations to their discovery settings, not a
// process-local View pointer or unrelated configured names and policy changes.
func namingScope(settings Settings) string {
	settings.MDNS.Interfaces = slices.Clone(settings.MDNS.Interfaces)
	if len(settings.MDNS.Interfaces) == 0 {
		settings.MDNS.Interfaces = nil
	}
	slices.Sort(settings.MDNS.Interfaces)
	// Keep the existing on-disk scope format and exclude the independently
	// versioned DNS catalogue from local-discovery cache compatibility.
	discovery := struct {
		Resolver  string
		HostsFile string
		MDNS      MDNSSettings
	}{settings.Resolver, settings.HostsFile, settings.MDNS}
	b, _ := json.Marshal(discovery)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func compatibleNames(a, b *View) bool {
	return a != nil && b != nil && a.scope == b.scope
}

func multicastName(n Name) bool {
	return n.Source == "mdns" || n.Source == "dns-sd" || n.Source == "spotify-connect"
}

func persistableName(n Name, now time.Time) bool {
	if !n.Address.IsValid() || n.Address.IsUnspecified() || n.Address.IsMulticast() || n.Address.IsLinkLocalUnicast() || n.Address.Zone() != "" ||
		n.Name == "" || n.Negative || n.Updated.IsZero() || n.Updated.After(now) {
		return false
	}
	if multicastName(n) {
		return retainDiscovered(n, now)
	}
	return (n.Source == "hosts" || n.Source == "router-ptr") && now.Before(n.Expires)
}

// NameSnapshot returns only derived positive names, with original confirmation
// times. It performs no IO and never refreshes a retention deadline.
func (m *Manager) NameSnapshot(now time.Time) (string, []Name) {
	v := m.current()
	if v == nil {
		return "", nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]Name, 0, len(m.mdnsNames)+len(m.cache))
	for _, cache := range []map[netip.Addr]entry{m.mdnsNames, m.cache} {
		for _, e := range cache {
			if compatibleNames(e.view, v) && persistableName(e.name, now) {
				n := e.name
				n.Device = cloneDevice(n.Device)
				if n.Device != nil {
					n.Device.DNSGuess = nil
				}
				names = append(names, n)
			}
		}
	}
	return v.scope, names
}

// RestoreNames is used before serving requests. Rehydration copies evidence,
// preserves TTLs, and cannot install configuration, lease authority or DNS guesses.
func (m *Manager) RestoreNames(scope string, names []Name, now time.Time) {
	v := m.current()
	if v == nil || scope != v.scope {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range names {
		if !persistableName(n, now) {
			continue
		}
		cache := m.cache
		if multicastName(n) {
			if !v.settings.MDNS.Enabled {
				continue
			}
			cache = m.mdnsNames
		}
		n.Address = n.Address.Unmap()
		if old, exists := cache[n.Address]; exists {
			if compatibleNames(old.view, v) && !n.Updated.After(old.name.Updated) {
				continue // A delayed storage recovery cannot replace newer discovery.
			}
		} else if len(cache) >= 4096 {
			continue
		}
		n.Device = cloneDevice(n.Device)
		if n.Device != nil {
			n.Device.DNSGuess = nil
		}
		cache[n.Address] = entry{view: v, name: n}
	}
}

// SetPersistenceError keeps storage failures observable without making DNS
// readiness depend on the disposable name cache.
func (m *Manager) SetPersistenceError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.persistenceError = ""
	if err != nil {
		m.persistenceError = "Client name persistence: " + err.Error()
	}
}
