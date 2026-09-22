package clients

import (
	"slices"
	"strings"
	"time"
)

// Evidence describes an address-verified local advertisement, not an authenticated identity.
type Evidence struct {
	Source       string    `json:"source"`
	Hostname     string    `json:"hostname,omitempty"`
	ServiceType  string    `json:"service_type,omitempty"`
	Label        string    `json:"label,omitempty"`
	Model        string    `json:"model,omitempty"`
	Manufacturer string    `json:"manufacturer,omitempty"`
	DeviceType   string    `json:"device_type,omitempty"`
	Updated      time.Time `json:"updated"`
	Expires      time.Time `json:"expires"`
}
type Enrichment struct {
	Category     string     `json:"category"`
	Reason       string     `json:"reason"`
	Inferred     bool       `json:"inferred"`
	Hostname     string     `json:"hostname,omitempty"`
	Model        string     `json:"model,omitempty"`
	Manufacturer string     `json:"manufacturer,omitempty"`
	Fresh        bool       `json:"fresh"`
	Evidence     []Evidence `json:"evidence"`
}

func cloneDevice(d *Enrichment) *Enrichment {
	if d == nil {
		return nil
	}
	c := *d
	c.Evidence = slices.Clone(d.Evidence)
	return &c
}

// Apply authoritative name selection last, even when discovery refreshes first.
func mergeDiscovered(primary, multicast Name, now time.Time) Name {
	primary.Device = cloneDevice(primary.Device)
	if multicast.Device != nil && len(multicast.Device.Evidence) > 0 {
		multicast = enrichDiscovered(multicast, now)
	}
	if now.Before(multicast.Expires) {
		staleDerived := !primary.Fresh && (primary.Source == "hosts" || primary.Source == "router-ptr")
		if primary.Name == "" || staleDerived {
			primary.Name = multicast.Name
			primary.Source = multicast.Source
			primary.Updated = multicast.Updated
			primary.Expires = multicast.Expires
			primary.Fresh = multicast.Fresh
			primary.Negative = multicast.Name == ""
		}
		primary.Device = cloneDevice(multicast.Device)
	}
	if primary.Device == nil {
		category, reason, inferred := classifyDevice(primary.Name, nil)
		primary.Device = &Enrichment{Category: category, Reason: reason, Inferred: inferred, Fresh: primary.Fresh, Evidence: []Evidence{}}
	}
	if primary.Device.Category == "unknown" && primary.Name != "" {
		category, reason, inferred := classifyDevice(primary.Name, primary.Device.Evidence)
		if category != "unknown" {
			primary.Device.Category = category
			primary.Device.Reason = reason
			primary.Device.Inferred = inferred
		}
	}
	return primary
}

func genericName(name string) bool {
	n := strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(name), "."), ".local")
	// mDNS conflict resolution appends numeric suffixes to generic hostnames.
	if i := strings.LastIndexAny(n, "-# "); i >= 0 && i+1 < len(n) && strings.Trim(n[i+1:], "0123456789") == "" {
		n = strings.TrimRight(n[:i], " #")
	}
	switch n {
	case "", "android", "linux", "home", "unknown", "device", "localhost", "none", "spotifyconnect", "amazon":
		return true
	}
	// Opaque service UUIDs and raw hex identifiers are poor display labels.
	if len(n) >= 16 && strings.Trim(n, "0123456789abcdef-") == "" {
		return true
	}
	// Cast and debugging services often prefix a human-readable model to an
	// opaque identifier. Those are still service IDs, not useful device labels.
	if i := strings.LastIndexByte(n, '-'); i >= 0 && len(n[i+1:]) >= 16 && strings.Trim(n[i+1:], "0123456789abcdef") == "" {
		return true
	}
	return false
}

func enrichDiscovered(n Name, now time.Time) Name {
	if !now.Before(n.Expires) {
		return Name{Address: n.Address, Source: "unknown"}
	}
	d := cloneDevice(n.Device)
	if d == nil {
		d = &Enrichment{Hostname: n.Name}
	}
	d.Evidence = slices.DeleteFunc(d.Evidence, func(e Evidence) bool { return !now.Before(e.Expires) })
	slices.SortFunc(d.Evidence, func(a, b Evidence) int {
		return strings.Compare(a.Label+"\x00"+a.ServiceType+"\x00"+a.Hostname, b.Label+"\x00"+b.ServiceType+"\x00"+b.Hostname)
	})
	if len(d.Evidence) > 0 {
		// A cached display label may have depended on now-expired TXT metadata.
		// Re-select from live evidence on every read, retaining the hostname when
		// it is still supported so packet ordering cannot make it oscillate.
		host := ""
		for _, e := range d.Evidence {
			if e.Hostname == "" {
				continue
			}
			if host == "" || e.Hostname == d.Hostname {
				host = e.Hostname
			}
			if host == d.Hostname {
				break
			}
		}
		if host != "" {
			n.Name, d.Hostname = host, host
		}
		n.Source = d.Evidence[0].Source
		d.Model, d.Manufacturer = "", ""
	}
	d.Fresh = true
	for _, e := range d.Evidence {
		if genericName(n.Name) && e.Label != "" && !genericName(e.Label) {
			n.Name = e.Label
			n.Source = "dns-sd"
		}
		if d.Model == "" {
			d.Model = e.Model
		}
		if d.Manufacturer == "" {
			d.Manufacturer = e.Manufacturer
		}
	}
	d.Category, d.Reason, d.Inferred = classifyDevice(d.Hostname, d.Evidence)
	n.Device = d
	n.Fresh = true
	n.Negative = n.Name == ""
	return n
}
