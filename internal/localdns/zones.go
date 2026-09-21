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
		if r.Match == "" {
			r.Match = "exact"
		}
		if r.Match != "exact" && r.Match != "suffix" {
			return nil, fmt.Errorf("local match must be exact or suffix")
		}
		if r.Match == "suffix" && (r.Type != "A" && r.Type != "AAAA" || r.AutoPTR) {
			return nil, fmt.Errorf("suffix rewrites require A/AAAA without auto_ptr")
		}
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
			if old := z.names[a.Unmap()]; r.Match != "suffix" && (old == "" || r.Name < old) {
				z.names[a.Unmap()] = r.Name
			}
			if r.AutoPTR {
				records = append(records, Record{Name: Reverse(a), Type: "PTR", Value: r.Name, TTL: r.TTL})
			}
		case "CNAME", "PTR":
			if zone := z.zone(r.Name); r.Type == "CNAME" && zone != nil && zone.Name == r.Name {
				return nil, fmt.Errorf("CNAME conflicts with zone apex SOA")
			}
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
			if old.Match != r.Match || old.Type == "CNAME" || r.Type == "CNAME" || old.Type == r.Type && old.Value == r.Value {
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

func (z *Zones) lookup(name string) []Record {
	if rr := z.records[name]; len(rr) > 0 {
		return rr
	}
	for strings.Contains(name, ".") {
		name = name[strings.IndexByte(name, '.')+1:]
		if rr := z.records[name]; len(rr) > 0 && rr[0].Match == "suffix" {
			return rr
		}
	}
	return nil
}

// Origin is explanation metadata, separate from adblock glob semantics.
func (z *Zones) Origin(name policy.Name) string {
	n := name.Display()
	rr := z.lookup(n)
	if len(rr) > 0 {
		if rr[0].Name != n {
			return "synthesized"
		}
		return "exact"
	}
	if z.zone(n) != nil {
		return "zone"
	}
	return "forward"
}

// Continuation returns the final nonlocal target of a configured CNAME chain.
// The result is independent wire storage; no forwarding is needed for CNAME
// questions or for a terminal owner inside our records/owned zones.
func (z *Zones) Continuation(q *dnswire.Message) []byte {
	if q.Question.Type == 5 {
		return nil
	}
	n, e := policy.NameFromWire(q.Question.Name.Canonical[:q.Question.Name.Length])
	if e != nil {
		return nil
	}
	name := n.Display()
	for depth := 0; depth <= 16; depth++ {
		rr := z.lookup(name)
		if len(rr) == 0 {
			if depth > 0 && z.zone(name) == nil {
				return wire(name)
			}
			return nil
		}
		if rr[0].Type != "CNAME" {
			return nil
		}
		name = rr[0].Value
	}
	return nil
}

func (z *Zones) Answer(dst []byte, q *dnswire.Message) (int, bool, error) {
	n, e := policy.NameFromWire(q.Question.Name.Canonical[:q.Question.Name.Length])
	if e != nil {
		return 0, false, e
	}
	name := n.Display()
	if zone := z.zone(name); zone != nil && zone.Name == name && q.Question.Type == 6 {
		size, err := dnswire.BuildSynthetic(dst, q, 0, true, []dnswire.SyntheticRecord{dnswire.NegativeSOA(wire(name), zone.NegativeTTL)}, nil)
		return size, true, err
	}
	if len(z.lookup(name)) == 0 && z.zone(name) == nil {
		return 0, false, nil
	}
	var answers, authority []dnswire.SyntheticRecord
	var code uint16
	for depth := 0; depth <= 16; depth++ {
		rr := z.lookup(name)
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
			owner := wire(name)
			if name == n.Display() {
				owner = q.Question.Name.Wire[:q.Question.Name.Length]
			}
			answers = append(answers, dnswire.SyntheticRecord{Name: owner, Type: typ, TTL: r.TTL, Data: data})
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
				if !z.exists[name] && len(rr) == 0 {
					code = 3
				}
				authority = append(authority, dnswire.NegativeSOA(wire(zone.Name), zone.NegativeTTL))
			} else if len(rr) > 0 {
				authority = append(authority, dnswire.NegativeSOA([]byte{0}, 2))
			}
		}
		break
	}
	size, err := dnswire.BuildSynthetic(dst, q, code, z.zone(n.Display()) != nil, answers, authority)
	return size, true, err
}
