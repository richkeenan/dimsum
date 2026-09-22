package dhcp

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"
)

type MessageType uint8

const (
	Discover       MessageType = 1
	Offer          MessageType = 2
	RequestMessage MessageType = 3
	Decline        MessageType = 4
	ACK            MessageType = 5
	NAK            MessageType = 6
	Release        MessageType = 7
	Inform         MessageType = 8
)

type LeaseState string

const (
	Offered       LeaseState = "offered"
	Probing       LeaseState = "probing"
	CommitPending LeaseState = "commit-pending"
	Bound         LeaseState = "bound"
	Quarantined   LeaseState = "quarantined"
)

// Request owns bounded data; ClientID is the opaque bytes, not hexadecimal.
// Packet adapters must reject non-Ethernet BOOTP and malformed options first.
type Request struct {
	Type                                   MessageType
	XID                                    uint32
	MAC                                    [6]byte
	ClientID                               string
	Hostname                               string
	RequestedIP, ServerID, CIAddr, RelayIP netip.Addr
	Broadcast                              bool
	MaxMessageSize                         uint16
	RequestedOptions                       [256]bool
}
type Reply struct {
	Type         MessageType
	Address      netip.Addr
	Request      Request
	LeaseSeconds int
}

// Token is unique for the lifetime of an engine, including configuration changes.
type Token struct{ Generation, Sequence uint64 }
type Probe struct {
	Token    Token
	Address  netip.Addr
	Deadline time.Time
}

// Err means no reliable probe was completed; it must never produce an OFFER.
type ProbeResult struct {
	Token    Token
	Conflict bool
	Err      error
}
type MutationKind string

const (
	PutLease    MutationKind = "put"
	DeleteLease MutationKind = "delete"
)

type Lease struct {
	Identity string
	MAC      [6]byte
	Address  netip.Addr
	Hostname string
	Expiry   time.Time
	State    LeaseState
}

// Mutation is an immutable value handed to a bounded durable writer. PutLease
// replaces the row by address; DeleteLease removes it. Return failure even on
// admission rejection. Never invoke CompleteCommit until the transaction ends.
type Mutation struct {
	Token Token
	Kind  MutationKind
	Lease Lease
}
type CommitResult struct {
	Token Token
	Err   error
}
type Outcome struct {
	Reply    *Reply
	Probe    *Probe
	Mutation *Mutation
}

type entry struct {
	lease    Lease
	request  Request
	token    Token
	deadline time.Time
	attempts int
	durable  bool
	previous Lease
	pending  Lease
	mutation MutationKind
	lastACK  uint32
	hasACK   bool
}

// Engine is owned by one event loop. All methods, including reads and completion
// callbacks, must run on that owner. It opens no sockets, timers or goroutines.
// At most two probe jobs and Capacity commit jobs may be outstanding. Call Tick
// from one shared scheduler and dispatch its bounded mutations without waiting.
type Engine struct {
	settings             Settings
	generation, sequence uint64
	clock                func() time.Time
	lastTime             time.Time
	suspended            bool
	byIP                 map[netip.Addr]*entry
	byID                 map[string]*entry
	reservations         map[string]Reservation
	reserved             map[netip.Addr]string
	bitmap               []uint64
	start, end           uint32
	probes               int
}

func NewEngine(s Settings, generation uint64, clock func() time.Time) (*Engine, error) {
	if err := ValidateSettings(s); err != nil {
		return nil, err
	}
	if generation == 0 {
		return nil, fmt.Errorf("dhcp: generation must be nonzero")
	}
	if clock == nil {
		clock = time.Now
	}
	e := &Engine{clock: clock, byIP: make(map[netip.Addr]*entry), byID: make(map[string]*entry)}
	e.install(s, generation)
	return e, nil
}
func (e *Engine) install(s Settings, g uint64) {
	e.settings = s.Clone()
	e.generation = g
	e.reservations = make(map[string]Reservation)
	e.reserved = make(map[netip.Addr]string)
	for _, r := range s.Reservations {
		k, _ := reservationKey(r)
		e.reservations[k] = r
		e.reserved[netip.MustParseAddr(r.Address)] = k
	}
	e.bitmap = nil
	e.start = 0
	e.end = 0
	a, ae := netip.ParseAddr(s.RangeStart)
	b, be := netip.ParseAddr(s.RangeEnd)
	if ae == nil && be == nil && a.Is4() && b.Is4() {
		e.start = ipv4Number(a)
		e.end = ipv4Number(b)
		e.bitmap = make([]uint64, (uint64(e.end)-uint64(e.start)+64)/64)
		for ip := range e.reserved {
			e.mark(ip, true)
		}
		for ip := range e.byIP {
			e.mark(ip, true)
		}
	}
}
func (e *Engine) mark(ip netip.Addr, used bool) {
	if len(e.bitmap) == 0 || !ip.Is4() {
		return
	}
	n := ipv4Number(ip)
	if n < e.start || n > e.end {
		return
	}
	i := n - e.start
	if used {
		e.bitmap[i/64] |= uint64(1) << (i % 64)
	} else if _, reserved := e.reserved[ip]; !reserved {
		e.bitmap[i/64] &^= uint64(1) << (i % 64)
	}
}
func (e *Engine) now() (time.Time, bool) {
	n := e.clock()
	if !e.lastTime.IsZero() && n.UnixNano() < e.lastTime.UnixNano() {
		e.suspended = true
		return n, false
	}
	e.lastTime = n
	e.suspended = false
	return n, true
}
func (e *Engine) ClockSuspended() bool { return e.suspended }
func (e *Engine) token() Token         { e.sequence++; return Token{e.generation, e.sequence} }
func identity(r Request) string {
	if r.ClientID != "" {
		return "id:" + r.ClientID
	}
	return "mac:" + string(r.MAC[:])
}
func validRequest(r Request) bool {
	if len(r.ClientID) > 255 || len(r.Hostname) > 63 || r.MAC[0]&1 != 0 || r.MAC == [6]byte{} || r.RelayIP.IsValid() && !r.RelayIP.IsUnspecified() {
		return false
	}
	for _, a := range []netip.Addr{r.RequestedIP, r.ServerID, r.CIAddr} {
		if a.IsValid() && !a.Is4() {
			return false
		}
	}
	return r.MaxMessageSize == 0 || r.MaxMessageSize >= 576
}
func nonzero(a netip.Addr) bool { return a.IsValid() && !a.IsUnspecified() }
func (e *Engine) reservation(r Request) (Reservation, bool) {
	if r.ClientID != "" {
		if v, ok := e.reservations["id:"+r.ClientID]; ok {
			return v, true
		}
	}
	v, ok := e.reservations["mac:"+string(r.MAC[:])]
	return v, ok
}
func (e *Engine) reply(kind MessageType, ip netip.Addr, r Request) *Reply {
	seconds := 0
	if ip.IsValid() {
		seconds = e.settings.LeaseSeconds
	}
	return &Reply{Type: kind, Address: ip, Request: r, LeaseSeconds: seconds}
}

func (e *Engine) ack(v *entry, r Request, now time.Time) Outcome {
	seconds := int(v.lease.Expiry.Sub(now) / time.Second)
	if seconds < 1 {
		return Outcome{}
	}
	reply := e.reply(ACK, v.lease.Address, r)
	reply.LeaseSeconds = seconds
	return Outcome{Reply: reply}
}
func (e *Engine) remove(v *entry) {
	delete(e.byIP, v.lease.Address)
	if e.byID[v.lease.Identity] == v {
		delete(e.byID, v.lease.Identity)
	}
	e.mark(v.lease.Address, false)
}
func (e *Engine) occupied() int {
	n := len(e.byIP)
	for ip := range e.reserved {
		if e.byIP[ip] == nil {
			n++
		}
	}
	return n
}
func (e *Engine) candidate(r Request) netip.Addr {
	if res, ok := e.reservation(r); ok {
		ip := netip.MustParseAddr(res.Address)
		if e.byIP[ip] == nil {
			return ip
		}
		return netip.Addr{}
	}
	if e.occupied() >= e.settings.Capacity() {
		return netip.Addr{}
	}
	// A requested free address is a hint only, never authority to steal ownership.
	if r.RequestedIP.Is4() {
		n := ipv4Number(r.RequestedIP)
		if n >= e.start && n <= e.end && e.bitmap[(n-e.start)/64]&(uint64(1)<<((n-e.start)%64)) == 0 {
			return r.RequestedIP
		}
	}
	for n := uint64(e.start); len(e.bitmap) > 0 && n <= uint64(e.end); n++ {
		i := uint32(n) - e.start
		if e.bitmap[i/64]&(uint64(1)<<(i%64)) == 0 {
			return numberIPv4(uint32(n))
		}
	}
	return netip.Addr{}
}
func (e *Engine) discover(r Request, now time.Time, attempt int) Outcome {
	if e.probes >= 2 || attempt > 3 {
		return Outcome{}
	}
	ip := e.candidate(r)
	if !ip.IsValid() {
		return Outcome{}
	}
	if e.byIP[ip] == nil {
		extra := 1
		if _, ok := e.reserved[ip]; ok {
			extra = 0
		}
		if e.occupied()+extra > e.settings.Capacity() {
			return Outcome{}
		}
	}
	name := strings.ToLower(r.Hostname)
	if !validLabel(name) {
		name = ""
	}
	if res, ok := e.reservation(r); ok && res.Hostname != "" {
		name = strings.ToLower(res.Hostname)
	}
	v := &entry{lease: Lease{Identity: identity(r), MAC: r.MAC, Address: ip, Hostname: name, State: Probing}, request: r, token: e.token(), deadline: now.Add(500 * time.Millisecond), attempts: attempt}
	e.byIP[ip] = v
	e.byID[v.lease.Identity] = v
	e.mark(ip, true)
	e.probes++
	return Outcome{Probe: &Probe{Token: v.token, Address: ip, Deadline: v.deadline}}
}
func (e *Engine) mutate(v *entry, kind MutationKind, target Lease, r Request) Outcome {
	v.previous = v.lease
	v.pending = target
	v.lease.State = CommitPending
	v.token = e.token()
	v.mutation = kind
	v.request = r
	return Outcome{Mutation: &Mutation{Token: v.token, Kind: kind, Lease: target}}
}

func (e *Engine) Handle(r Request) Outcome {
	now, ok := e.now()
	if !ok || !e.settings.Enabled || !validRequest(r) {
		return Outcome{}
	}
	server := netip.MustParseAddr(e.settings.ServerIP)
	if nonzero(r.ServerID) && r.ServerID != server {
		return Outcome{}
	}
	id := identity(r)
	v := e.byID[id]
	if v != nil && v.lease.State == CommitPending {
		return Outcome{}
	}
	if v != nil && (v.lease.State == Offered || v.lease.State == Bound) && !now.Before(v.lease.Expiry) {
		return Outcome{}
	} // Tick must retire ownership first.
	switch r.Type {
	case Discover:
		if v != nil {
			switch v.lease.State {
			case Bound, Offered:
				v.request = r
				return Outcome{Reply: e.reply(Offer, v.lease.Address, r)}
			case Probing:
				v.request = r
				return Outcome{}
			default:
				return Outcome{}
			}
		}
		return e.discover(r, now, 1)
	case RequestMessage:
		addr := r.RequestedIP
		if nonzero(r.CIAddr) {
			if nonzero(r.RequestedIP) || nonzero(r.ServerID) {
				return Outcome{}
			}
			addr = r.CIAddr
		}
		if !nonzero(addr) {
			return Outcome{}
		}
		if !netip.MustParsePrefix(e.settings.Subnet).Contains(addr) {
			return Outcome{Reply: e.reply(NAK, netip.Addr{}, r)}
		}
		if v == nil || v.lease.Address != addr || v.lease.State != Bound && v.lease.State != Offered {
			// Unknown on-link INIT-REBOOT is ignored; known conflicting ownership and
			// off-subnet addresses are authoritative NAK cases.
			p := netip.MustParsePrefix(e.settings.Subnet)
			if !p.Contains(addr) || e.byIP[addr] != nil && e.byIP[addr].lease.Identity != id {
				return Outcome{Reply: e.reply(NAK, netip.Addr{}, r)}
			}
			return Outcome{}
		}
		if nonzero(r.ServerID) && v.lease.State == Offered && v.request.XID != r.XID {
			return Outcome{}
		}
		if v.lease.State == Offered && !nonzero(r.ServerID) {
			return Outcome{}
		}
		if v.lease.State == Bound && v.hasACK && v.lastACK == r.XID {
			return e.ack(v, r, now)
		}
		target := v.lease
		target.MAC = r.MAC
		target.State = Bound
		target.Expiry = now.Add(time.Duration(e.settings.LeaseSeconds) * time.Second)
		out := e.mutate(v, PutLease, target, r)
		v.deadline = target.Expiry
		return out
	case Release:
		if v == nil || v.lease.State != Bound || r.CIAddr != v.lease.Address {
			return Outcome{}
		}
		return e.mutate(v, DeleteLease, v.lease, r)
	case Decline:
		if r.ServerID != server || v == nil || v.lease.Address != r.RequestedIP || v.lease.State != Bound && v.lease.State != Offered {
			return Outcome{}
		}
		if v.durable {
			target := v.lease
			target.State = Quarantined
			target.Expiry = now.Add(10 * time.Minute)
			out := e.mutate(v, PutLease, target, r)
			v.deadline = target.Expiry
			return out
		}
		e.quarantine(v, now)
		return Outcome{}
	case Inform:
		if nonzero(r.CIAddr) {
			return Outcome{Reply: e.reply(ACK, netip.Addr{}, r)}
		}
	}
	return Outcome{}
}
func (e *Engine) quarantine(v *entry, now time.Time) {
	if e.byID[v.lease.Identity] == v {
		delete(e.byID, v.lease.Identity)
	}
	v.lease.State = Quarantined
	v.lease.Expiry = now.Add(10 * time.Minute)
}
func (e *Engine) find(t Token) *entry {
	if t.Generation != e.generation {
		return nil
	}
	for _, v := range e.byIP {
		if v.token == t {
			return v
		}
	}
	return nil
}
func (e *Engine) CompleteProbe(result ProbeResult) Outcome {
	now, ok := e.now()
	v := e.find(result.Token)
	if v == nil || v.lease.State != Probing {
		return Outcome{}
	}
	e.probes--
	if !ok || result.Err != nil || !now.Before(v.deadline) {
		e.remove(v)
		return Outcome{}
	}
	if result.Conflict {
		r, attempt := v.request, v.attempts
		e.quarantine(v, now)
		return e.discover(r, now, attempt+1)
	}
	v.lease.State = Offered
	v.lease.Expiry = now.Add(30 * time.Second)
	return Outcome{Reply: e.reply(Offer, v.lease.Address, v.request)}
}
func (e *Engine) CompleteCommit(result CommitResult) Outcome {
	now, clockOK := e.now()
	v := e.find(result.Token)
	if v == nil || v.lease.State != CommitPending {
		return Outcome{}
	}
	if result.Err != nil {
		v.lease = v.previous
		return Outcome{}
	}
	if v.mutation == DeleteLease {
		e.remove(v)
		return Outcome{}
	}
	v.lease = v.pending
	v.durable = true
	if v.request.Type == Decline {
		v.lease.State = Quarantined
		delete(e.byID, v.lease.Identity)
		return Outcome{}
	}
	v.lease.State = Bound
	v.hasACK = true
	v.lastACK = v.request.XID
	if !clockOK || !now.Before(v.lease.Expiry) || !e.settings.Enabled {
		return Outcome{}
	}
	return e.ack(v, v.request, now)
}

// Tick uses one bounded scan. Durable expiry is deleted before address reuse;
// failed or unadmitted deletes retain ownership and are retried on a later Tick.
func (e *Engine) Tick() []Mutation {
	now, ok := e.now()
	if !ok {
		return nil
	}
	var out []Mutation
	for _, l := range e.Leases() {
		v := e.byIP[l.Address]
		switch v.lease.State {
		case Probing:
			if !now.Before(v.deadline) {
				e.probes--
				e.remove(v)
			}
		case Offered, Bound, Quarantined:
			if !now.Before(v.lease.Expiry) {
				if v.durable {
					m := e.mutate(v, DeleteLease, v.lease, Request{})
					out = append(out, *m.Mutation)
				} else {
					e.remove(v)
				}
			}
		}
	}
	return out
}

// Leases returns a bounded copy in address order, including transient ownership.
func (e *Engine) Leases() []Lease {
	out := make([]Lease, 0, len(e.byIP))
	for _, v := range e.byIP {
		out = append(out, v.lease)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address.Less(out[j].Address) })
	return out
}

// DurableLeases returns committed ownership in address order for persistence/DNS
// publication. Pending renewals/deletions retain their previous durable value;
// uncommitted allocations never appear. Consumers still filter state and expiry.
func (e *Engine) DurableLeases() []Lease {
	out := make([]Lease, 0, len(e.byIP))
	for _, v := range e.byIP {
		if !v.durable {
			continue
		}
		l := v.lease
		if l.State == CommitPending {
			l = v.previous
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address.Less(out[j].Address) })
	return out
}

// Apply is atomic. Drain durable mutations before switching generations; a
// writer might already have committed even if its completion is still queued.
// Probes/offers are disposable; durable ownership and quarantines are retained.
func (e *Engine) Apply(s Settings, generation uint64) error {
	if generation <= e.generation {
		return fmt.Errorf("dhcp: generation must increase")
	}
	if err := ValidateTransition(e.settings, s); err != nil {
		return err
	}
	for _, v := range e.byIP {
		if v.lease.State == CommitPending {
			return fmt.Errorf("dhcp: durable mutation pending; drain before applying")
		}
	}
	keep := map[netip.Addr]*entry{}
	for ip, v := range e.byIP {
		if v.durable || v.lease.State == Quarantined {
			keep[ip] = v
		}
	}
	occupied := len(keep)
	for _, r := range s.Reservations {
		ip := netip.MustParseAddr(r.Address)
		v := keep[ip]
		if v == nil {
			occupied++
			continue
		}
		key, _ := reservationKey(r)
		matches := key == v.lease.Identity || strings.HasPrefix(key, "mac:") && key == "mac:"+string(v.lease.MAC[:])
		if !matches {
			return fmt.Errorf("dhcp: reservation %s conflicts with lease %s at %s until %s", r.ID, v.lease.Identity, ip, v.lease.Expiry.Format(time.RFC3339))
		}
	}
	if occupied > s.Capacity() {
		return fmt.Errorf("dhcp: retained ownership and reservations exceed capacity")
	}
	for _, v := range e.byIP {
		if keep[v.lease.Address] == nil {
			e.remove(v)
		}
	}
	e.probes = 0
	for _, v := range e.byIP {
		v.hasACK = false
	}
	e.install(s, generation)
	return nil
}

// Restore installs an all-or-nothing recovery snapshot before accepting packets.
// The store must verify initialization/schema/integrity and supply its durable
// last-known wall time. Expired rows are retained until Tick durably deletes them.
// Pool/subnet changes do not revoke recovered ownership.
func (e *Engine) Restore(leases []Lease, lastKnown time.Time) error {
	if len(e.byIP) != 0 || e.sequence != 0 {
		return fmt.Errorf("dhcp: recovery requires unused engine")
	}
	now := e.clock()
	if now.UnixNano() < lastKnown.UnixNano() {
		return fmt.Errorf("dhcp: clock precedes durable last-known time")
	}
	if len(leases) > e.settings.Capacity() {
		return fmt.Errorf("dhcp: recovered state exceeds capacity")
	}
	ips := make(map[netip.Addr]*entry)
	ids := make(map[string]*entry)
	for _, l := range leases {
		keyOK := strings.HasPrefix(l.Identity, "id:") && len(l.Identity) > 3 && len(l.Identity) <= 258 || l.Identity == "mac:"+string(l.MAC[:])
		if !keyOK || l.MAC == [6]byte{} || l.MAC[0]&1 != 0 || !l.Address.Is4() || l.Address.IsUnspecified() || l.Address.IsMulticast() || l.Address.IsLoopback() || l.Address.As4()[0] >= 240 || l.Expiry.IsZero() || len(l.Hostname) > 63 || l.Hostname != "" && !validLabel(l.Hostname) || l.State != Bound && l.State != Quarantined || ips[l.Address] != nil {
			return fmt.Errorf("dhcp: invalid recovered lease")
		}
		v := &entry{lease: l, durable: true}
		ips[l.Address] = v
		if l.State == Bound {
			if ids[l.Identity] != nil {
				return fmt.Errorf("dhcp: duplicate recovered identity")
			}
			ids[l.Identity] = v
		}
	}
	occupied := len(ips)
	for ip, key := range e.reserved {
		if v := ips[ip]; v != nil {
			if key != v.lease.Identity && key != "mac:"+string(v.lease.MAC[:]) {
				return fmt.Errorf("dhcp: recovered lease conflicts with reservation at %s", ip)
			}
		} else {
			occupied++
		}
	}
	if occupied > e.settings.Capacity() {
		return fmt.Errorf("dhcp: recovered ownership and reservations exceed capacity")
	}
	e.byIP = ips
	e.byID = ids
	e.lastTime = now
	for ip := range ips {
		e.mark(ip, true)
	}
	return nil
}
