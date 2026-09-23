package clients

import (
	"slices"
	"strings"
	"time"
)

// Evidence describes an address-verified local advertisement, not an authenticated identity.
type Evidence struct {
	spotify      spotifyEndpoint
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
	DNSGuess     *DNSGuess  `json:"dns_guess,omitempty"`
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
	if d.DNSGuess != nil {
		guess := *d.DNSGuess
		guess.Domains = slices.Clone(guess.Domains)
		c.DNSGuess = &guess
	}
	return &c
}

// Apply authoritative name selection last, even when discovery refreshes first.
func mergeDiscovered(primary, multicast Name, now time.Time) Name {
	primary.Device = cloneDevice(primary.Device)
	remembered := multicast
	if multicast.Device != nil && len(multicast.Device.Evidence) > 0 {
		multicast = enrichDiscovered(multicast, now)
	}
	if !now.Before(multicast.Expires) {
		multicast = Name{}
	}
	multicast = preferDiscovered(remembered, multicast, now)
	if multicast.Name != "" {
		staleDerived := !primary.Fresh && (primary.Source == "hosts" || primary.Source == "router-ptr")
		if primary.Name == "" || staleDerived && multicast.Fresh {
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

const discoveredNameRetention = 48 * time.Hour

func retainDiscovered(n Name, now time.Time) bool {
	return n.Name != "" && !n.Updated.IsZero() && now.Before(n.Updated.Add(discoveredNameRetention))
}

// Preserve useful identity when records disappear, but accept changed hostnames,
// friendly names, and conflicting positive device information immediately.
// Reads and weaker hostname-only renewals cannot extend the remembered deadline.
func preferDiscovered(previous, live Name, now time.Time) Name {
	if !retainDiscovered(previous, now) {
		return live
	}
	missing := live.Name == ""
	p, l := previous.Device, live.Device
	if p != nil && l != nil && p.Hostname != "" && p.Hostname == l.Hostname &&
		!live.Updated.After(previous.Updated) && (previous.Name != live.Name ||
		p.Category != l.Category || p.Model != l.Model || p.Manufacturer != l.Manufacturer) {
		// Re-selecting among already-known records as some expire is not a
		// confirmation of a new identity, even if the fallback looks specific.
		missing = true
	} else if p != nil && l != nil && p.Hostname != "" && p.Hostname == l.Hostname &&
		(live.Name == previous.Name || live.Name == l.Hostname) {
		conflict := l.Category != "" && l.Category != "unknown" && p.Category != l.Category ||
			l.Model != "" && p.Model != "" && l.Model != p.Model ||
			l.Manufacturer != "" && p.Manufacturer != "" && l.Manufacturer != p.Manufacturer
		missing = (!p.Inferred && l.Inferred) || !conflict && (previous.Name != live.Name ||
			p.Category != "" && p.Category != "unknown" && (l.Category == "" || l.Category == "unknown") ||
			p.Model != "" && l.Model == "" || p.Manufacturer != "" && l.Manufacturer == "")
	}
	if !missing {
		return live
	}
	previous.Fresh = false
	previous.Device = cloneDevice(previous.Device)
	if previous.Device != nil {
		previous.Device.Fresh = false
	}
	return previous
}

func genericName(name string) bool {
	n := strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(name), "."), ".local")
	// Check opaque IDs before conflict suffixes: an ID may contain only digits.
	if i := strings.LastIndexByte(n, '-'); i >= 0 && len(n[i+1:]) >= 16 && strings.Trim(n[i+1:], "0123456789abcdef") == "" {
		return true
	}
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
	return false
}

// Device-naming services outrank hostnames; arbitrary application labels only
// replace generic hostnames. Evidence has already been address-verified by the
// discovery graph. Preserve its raw label for inspection, including RAOP IDs.
func discoveredLabel(e Evidence) (string, int) {
	if e.Source != "dns-sd" && e.Source != "spotify-connect" {
		return "", 0
	}
	label := safeLabel(e.Label)
	priority := 1
	if e.Source == "spotify-connect" {
		priority = 4
		// Prefer the verified product over the console's default-looking
		// numbered name. Keep personalised labels and raw evidence intact.
		if e.Manufacturer == "Sony" && e.Model == "PlayStation 5" {
			if suffix, ok := strings.CutPrefix(strings.ToLower(label), "ps5-"); ok && suffix != "" && strings.Trim(suffix, "0123456789") == "" {
				label = "Sony PlayStation 5"
			}
		}
	}
	switch strings.TrimSuffix(e.ServiceType, ".") {
	case "_airplay._tcp", "_companion-link._tcp", "_googlecast._tcp", "_ipp._tcp", "_ipps._tcp", "_ipp-tls._tcp", "_printer._tcp", "_pdl-datastream._tcp":
		priority = 4
	case "_raop._tcp":
		id, friendly, ok := strings.Cut(label, "@")
		if !ok || len(id) != 12 || strings.Trim(strings.ToLower(id), "0123456789abcdef") != "" {
			return "", 0
		}
		label, priority = safeLabel(friendly), 3
	}
	if label == "" || genericName(label) {
		if e.Manufacturer != "" && e.Model != "" {
			return e.Manufacturer + " " + e.Model, 1
		}
		return "", 0
	}
	return label, priority
}

// Normalize known product spelling, never infer a generation or room name.
func deviceProduct(brand, model string) (string, string) {
	brand, model = safeLabel(brand), safeLabel(model)
	if (strings.EqualFold(brand, "sony_tv") || strings.EqualFold(brand, "sony")) && strings.EqualFold(model, "ps5") {
		return "Sony", "PlayStation 5"
	}
	if strings.EqualFold(brand, "amazon") {
		brand = "Amazon"
		switch strings.ToLower(model) {
		case "echo":
			model = "Echo"
		case "echo_dot":
			model = "Echo Dot"
		}
	}
	return brand, model
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
	priority := 2 // A useful hostname beats an arbitrary application's label.
	if genericName(n.Name) {
		priority = 0
	}
	for _, e := range d.Evidence {
		label, rank := discoveredLabel(e)
		if rank > 0 && (rank > priority || (rank == priority && label < n.Name)) {
			n.Name, priority = label, rank
			n.Source = e.Source
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
