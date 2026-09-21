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
	if now.Before(multicast.Expires) {
		if primary.Name == "" {
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
	return primary
}

func genericName(name string) bool {
	n := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(name, "."), ".local"))
	switch n {
	case "", "android", "linux", "home", "unknown", "device", "localhost", "none", "none-2":
		return true
	}
	// Opaque service UUIDs and raw hex identifiers are poor display labels.
	if len(n) >= 16 && strings.Trim(n, "0123456789abcdef-") == "" {
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
