package localdns

import (
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/richkeenan/dimsum/internal/dnswire"
)

// Lease is detached committed IPv4 ownership. Expiry, never the conservative
// ownership hold time, bounds DNS and dynamic display attribution. Reservation
// display names remain explicit configuration after their lease expires.
type Lease struct {
	Address     netip.Addr
	Hostname    string
	Expiry      time.Time
	Generated   bool
	Reservation bool
}
type leaseRecord struct {
	lease      Lease
	name, data []byte
	typ        uint16
}

// Leases is an immutable, generation-compatible publication separate from policy
// and the upstream cache. All wire data is prepared on publication, not lookup.
type Leases struct {
	generation uint64
	domain     string
	domainWire []byte
	records    map[string]leaseRecord
	names      map[netip.Addr]Lease
	negative   [31]dnswire.SyntheticRecord
}

func (v *Leases) Generation() uint64 { return v.generation }

func leaseLabel(s string) bool {
	if len(s) == 0 || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func addressLabel(s string) bool {
	if !strings.HasPrefix(s, "host-") {
		return false
	}
	parts := strings.Split(s[5:], "-")
	if len(parts) < 4 {
		return false
	}
	a, err := netip.ParseAddr(strings.Join(parts[:4], "."))
	return err == nil && a.Is4()
}

// BuildLeases consumes validated DHCP configuration and detached committed rows
// once per source publication/configuration.
// Duplicate labels all fall back, so names do not depend on iteration order.
func BuildLeases(generation uint64, domain string, rows []Lease, reservations map[netip.Addr]string, explicit *Zones) *Leases {
	domain = strings.TrimSuffix(strings.ToLower(domain), ".")
	v := &Leases{generation: generation, domain: domain, domainWire: wire(domain), records: make(map[string]leaseRecord), names: make(map[netip.Addr]Lease)}
	for ttl := range v.negative {
		v.negative[ttl] = dnswire.NegativeSOA(v.domainWire, uint32(ttl))
	}
	reserved := make(map[string]netip.Addr)
	counts := make(map[string]int)
	for a, n := range reservations {
		if n != "" {
			reserved[strings.ToLower(n)] = a
		}
	}
	for _, l := range rows {
		label := l.Hostname
		if n := reservations[l.Address]; n != "" {
			label = n
		}
		counts[strings.ToLower(label)]++
	}
	conflict := func(n string) bool {
		return explicit != nil && (len(explicit.lookup(n+"."+domain)) > 0 || explicit.exists[n+"."+domain])
	}
	for _, l := range rows {
		l.Generated, l.Reservation = false, false
		if !l.Address.Is4() {
			continue
		}
		label := strings.ToLower(l.Hostname)
		if n := reservations[l.Address]; n != "" {
			label = strings.ToLower(n)
			l.Reservation = true
		}
		owner, held := reserved[label]
		if !leaseLabel(label) || conflict(label) || !l.Reservation && (counts[label] > 1 || held && owner != l.Address || addressLabel(label)) {
			base := "host-" + strings.ReplaceAll(l.Address.String(), ".", "-")
			label = base
			l.Generated = true
			l.Reservation = false
			// Suffix rewrites can cover every possible fallback. They retain
			// answer precedence; only exact owners constrain fallback selection.
			for i := 1; explicit != nil && explicit.exists[label+"."+domain] || reserved[label].IsValid(); i++ {
				label = base + "-" + strconv.Itoa(i)
			}
		}
		l.Hostname = label + "." + domain
		v.names[l.Address] = l
		forward := wire(l.Hostname)
		addr := l.Address.As4()
		v.records[string(forward)] = leaseRecord{l, forward, append([]byte(nil), addr[:]...), 1}
		reverse := wire(Reverse(l.Address))
		v.records[string(reverse)] = leaseRecord{l, reverse, forward, 12}
	}
	// Reservation display names are explicit configuration, not evidence that
	// an address currently has a lease. DNS above still requires live ownership.
	for a, n := range reservations {
		if n == "" {
			continue
		}
		l := v.names[a]
		l.Address = a
		l.Hostname = strings.ToLower(n) + "." + domain
		l.Reservation = true
		l.Generated = false
		v.names[a] = l
	}
	return v
}

func (v *Leases) Name(a netip.Addr, now time.Time) Lease {
	if v != nil {
		l := v.names[a.Unmap()]
		if l.Reservation && !now.Before(l.Expiry) {
			l.Expiry = time.Time{}
			return l
		}
		if now.Before(l.Expiry) {
			return l
		}
	}
	return Lease{}
}
func (v *Leases) owns(name []byte) bool {
	if v == nil || len(name) < len(v.domainWire) || string(name[len(name)-len(v.domainWire):]) != string(v.domainWire) {
		return false
	}
	for i := 0; i < len(name) && name[i] != 0; i += int(name[i]) + 1 {
		if string(name[i:]) == string(v.domainWire) {
			return true
		}
	}
	return false
}
func (v *Leases) lookup(name []byte, typ uint16, now time.Time) (dnswire.SyntheticRecord, bool) {
	if v == nil {
		return dnswire.SyntheticRecord{}, false
	}
	r, ok := v.records[string(name)]
	if !ok || !now.Before(r.lease.Expiry) {
		return dnswire.SyntheticRecord{}, false
	}
	ttl := uint32(min(30, r.lease.Expiry.Sub(now)/time.Second))
	if typ != r.typ {
		return v.negative[ttl], true
	}
	return dnswire.SyntheticRecord{Name: r.name, Type: r.typ, TTL: ttl, Data: r.data}, true
}

// Answer is the allocation-free direct path when explicit local data is empty.
func (v *Leases) Answer(dst []byte, q *dnswire.Message, now time.Time) (int, bool, error) {
	name := q.Question.Name.Canonical[:q.Question.Name.Length]
	owned := v.owns(name)
	// Most traffic is neither in the lease domain nor IPv4 reverse. Reject it
	// before hashing into lease indexes; the wire suffix is label-checked by owns.
	const reverseSuffix = "\x07in-addr\x04arpa\x00"
	if !owned && (len(name) < len(reverseSuffix) || string(name[len(name)-len(reverseSuffix):]) != reverseSuffix) {
		return 0, false, nil
	}
	rr, ok := v.lookup(name, q.Question.Type, now)
	var answers, authority [1]dnswire.SyntheticRecord
	var a, ns []dnswire.SyntheticRecord
	code := uint16(0)
	if ok {
		if rr.Type == 6 {
			authority[0] = rr
			ns = authority[:]
		} else {
			rr.Name = q.Question.Name.Wire[:q.Question.Name.Length]
			answers[0] = rr
			a = answers[:]
		}
	} else if owned {
		code = 3
		if string(name) == string(v.domainWire) {
			code = 0
		}
		if code == 0 && q.Question.Type == 6 {
			answers[0] = v.negative[30]
			a = answers[:]
		} else {
			authority[0] = v.negative[30]
			ns = authority[:]
		}
	} else {
		return 0, false, nil
	}
	n, e := dnswire.BuildSynthetic(dst, q, code, owned, a, ns)
	return n, true, e
}
