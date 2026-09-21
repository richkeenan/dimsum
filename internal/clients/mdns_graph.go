package clients

import (
	"github.com/richkeenan/dimsum/internal/localdns"
	"net/netip"
	"slices"
	"strings"
	"time"
)

func (c *mdnsCache) lookup(address netip.Addr, now time.Time) Name {
	result := Name{Address: address, Source: "unknown"}
	// Observations currently carry no ingress interface. Link-local matches would
	// be ambiguous even if only one interface happened to advertise at this instant.
	if address.IsLinkLocalUnicast() {
		return result
	}
	hosts := map[string]mdnsRecord{}
	iface := 0
	c.index()
	for _, r := range c.addresses[address] {
		if r.address == address && now.Before(r.expires) {
			if iface != 0 && iface != r.key.iface {
				return result
			}
			iface = r.key.iface
			hosts[r.key.owner] = r
		}
	}
	if len(hosts) == 0 {
		return result
	}
	if len(hosts) > 16 {
		return result
	}
	evidence := []Evidence{}
	for host, a := range hosts {
		for _, p := range c.owners[recordGroup{iface, localdns.Reverse(address), 12}] {
			if p.target == host && now.Before(p.expires) {
				evidence = append(evidence, Evidence{Source: "mdns", Hostname: host, Updated: p.learned, Expires: minTime(a.expires, p.expires)})
			}
		}
		for _, srv := range c.targets[recordGroup{iface, host, 33}] {
			if !now.Before(srv.expires) {
				continue
			}
			for _, p := range c.targets[recordGroup{iface, srv.key.owner, 12}] {
				if !now.Before(p.expires) {
					continue
				}
				if !strings.HasSuffix(p.key.owner, "._tcp.local") && !strings.HasSuffix(p.key.owner, "._udp.local") {
					continue
				}
				e := Evidence{Source: "dns-sd", Hostname: host, ServiceType: strings.TrimSuffix(p.key.owner, ".local"), Label: p.label, Updated: srv.learned, Expires: minTime(a.expires, minTime(p.expires, srv.expires))}
				for _, txt := range c.owners[recordGroup{iface, srv.key.owner, 16}] {
					if txt.key.iface == iface && txt.key.owner == srv.key.owner && txt.key.kind == 16 && now.Before(txt.expires) {
						// Conflicting TXT records do not supply classification metadata.
						if e.Model != "" || e.Manufacturer != "" || e.DeviceType != "" {
							e.Model = ""
							e.Manufacturer = ""
							e.DeviceType = ""
							break
						}
						for _, key := range []string{"model", "md", "am", "ty", "product"} {
							if txt.txt[key] != "" {
								e.Model = txt.txt[key]
								break
							}
						}
						e.Manufacturer = txt.txt["manufacturer"]
						e.DeviceType = txt.txt["device_type"]
						e.Expires = minTime(e.Expires, txt.expires)
					}
				}
				evidence = append(evidence, e)
			}
		}
	}
	if len(evidence) == 0 {
		return result
	}
	slices.SortFunc(evidence, func(a, b Evidence) int {
		if a.Source != b.Source {
			if a.Source == "mdns" {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Hostname+"\x00"+a.ServiceType+"\x00"+a.Label, b.Hostname+"\x00"+b.ServiceType+"\x00"+b.Label)
	})
	if len(evidence) > 16 {
		evidence = evidence[:16]
	}
	first := evidence[0]
	result.Name = first.Hostname
	result.Source = first.Source
	result.Expires = first.Expires
	result.Updated = first.Updated
	result.Fresh = true
	for _, e := range evidence {
		result.Expires = minTime(result.Expires, e.Expires)
	}
	result.Device = &Enrichment{Hostname: first.Hostname, Evidence: evidence}
	return result
}
