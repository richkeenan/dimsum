package localdns

import (
	"fmt"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/policy"
	"net/netip"
	"sort"
	"strings"
)

type Zones struct {
	zones   []Zone
	records map[string][]Record
	exists  map[string]bool
	names   map[netip.Addr]string
}

func Build(zones []Zone, records []Record) (*Zones, error) {
	z := &Zones{records: map[string][]Record{}, exists: map[string]bool{}, names: map[netip.Addr]string{}}
	seen := map[string]bool{}
	for _, zone := range zones {
		n, e := normalized(zone.Name)
		if e != nil {
			return nil, e
		}
		if seen[n] {
			return nil, fmt.Errorf("duplicate zone %s", n)
		}
		seen[n] = true
		zone.Name = n
		z.zones = append(z.zones, zone)
		z.exists[n] = true
	}
	sort.Slice(z.zones, func(i, j int) bool { return len(z.zones[i].Name) > len(z.zones[j].Name) })
	records = append([]Record(nil), records...)
	for i := 0; i < len(records); i++ {
		r := records[i]
		var e error
		r.Name, e = normalized(r.Name)
		if e != nil {
			return nil, e
		}
		switch r.Type {
		case "A", "AAAA":
			a, err := netip.ParseAddr(r.Value)
			if err != nil || a.Zone() != "" || (r.Type == "A") != a.Is4() {
				return nil, fmt.Errorf("invalid %s address", r.Type)
			}
			if old := z.names[a.Unmap()]; old == "" || r.Name < old {
				z.names[a.Unmap()] = r.Name
			}
			if r.AutoPTR {
				records = append(records, Record{Name: Reverse(a), Type: "PTR", Value: r.Name, TTL: r.TTL})
			}
		case "CNAME", "PTR":
			r.Value, e = normalized(r.Value)
			if e != nil {
				return nil, e
			}
			if r.AutoPTR {
				return nil, fmt.Errorf("auto_ptr requires address record")
			}
		default:
			return nil, fmt.Errorf("unsupported local type %s", r.Type)
		}
		for _, old := range z.records[r.Name] {
			if old.Type == "CNAME" || r.Type == "CNAME" || old.Type == r.Type && old.Value == r.Value {
				return nil, fmt.Errorf("conflicting local owner %s", r.Name)
			}
		}
		z.records[r.Name] = append(z.records[r.Name], r)
		z.exists[r.Name] = true
		for parent := r.Name; strings.Contains(parent, "."); {
			parent = parent[strings.IndexByte(parent, '.')+1:]
			if z.zone(parent) != nil {
				z.exists[parent] = true
			}
		}
	}
	for name := range z.records {
		seen := map[string]bool{}
		for depth := 0; ; depth++ {
			rr := z.records[name]
			if len(rr) == 0 || rr[0].Type != "CNAME" {
				break
			}
			if seen[name] || depth >= 16 {
				return nil, fmt.Errorf("local CNAME cycle/depth at %s", name)
			}
			seen[name] = true
			name = rr[0].Value
		}
	}
	return z, nil
}
func (z *Zones) zone(name string) *Zone {
	for i := range z.zones {
		v := &z.zones[i]
		if name == v.Name || strings.HasSuffix(name, "."+v.Name) {
			return v
		}
	}
	return nil
}

// Names returns a copy: IP remains the identity even when several addresses share a name.
func (z *Zones) Names() map[netip.Addr]string {
	out := map[netip.Addr]string{}
	for k, v := range z.names {
		out[k] = v
	}
	return out
}

func (z *Zones) Answer(dst []byte, q *dnswire.Message) (int, bool, error) {
	n, e := policy.NameFromWire(q.Question.Name.Canonical[:q.Question.Name.Length])
	if e != nil {
		return 0, false, e
	}
	name := n.Display()
	if len(z.records[name]) == 0 && z.zone(name) == nil {
		return 0, false, nil
	}
	var answers, authority []dnswire.SyntheticRecord
	var code uint16
	for depth := 0; depth <= 16; depth++ {
		rr := z.records[name]
		alias := ""
		for _, r := range rr {
			typ := map[string]uint16{"A": 1, "AAAA": 28, "CNAME": 5, "PTR": 12}[r.Type]
			if typ != q.Question.Type && typ != 5 {
				continue
			}
			var data []byte
			if typ == 1 || typ == 28 {
				data = netip.MustParseAddr(r.Value).AsSlice()
			} else {
				data = wire(r.Value)
			}
			answers = append(answers, dnswire.SyntheticRecord{Name: wire(name), Type: typ, TTL: r.TTL, Data: data})
			if typ == 5 && q.Question.Type != 5 {
				alias = r.Value
			}
		}
		if alias != "" {
			name = alias
			continue
		}
		if len(rr) == 0 || len(answers) == 0 || answers[len(answers)-1].Type == 5 && q.Question.Type != 5 {
			if zone := z.zone(name); zone != nil {
				if !z.exists[name] {
					code = 3
				}
				authority = append(authority, dnswire.NegativeSOA(wire(zone.Name), zone.NegativeTTL))
			}
		}
		break
	}
	size, err := dnswire.BuildSynthetic(dst, q, code, z.zone(n.Display()) != nil, answers, authority)
	return size, true, err
}
