package clients

import (
	"context"
	"fmt"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/localdns"
	"math/rand/v2"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"
)

type MDNSSettings struct {
	Enabled    bool     `yaml:"enabled,omitempty"`
	Interfaces []string `yaml:"interfaces,omitempty"`
}

func (s MDNSSettings) validate() error {
	if len(s.Interfaces) > 8 {
		return fmt.Errorf("at most eight discovery interfaces")
	}
	seen := map[string]bool{}
	for _, n := range s.Interfaces {
		if n == "" || len(n) > 64 || strings.ContainsAny(n, " \t\r\n\x00") || seen[n] {
			return fmt.Errorf("invalid or duplicate discovery interface")
		}
		seen[n] = true
	}
	return nil
}

type DiscoveryDiagnostics struct {
	Enabled            bool     `json:"enabled"`
	Running            bool     `json:"running"`
	Interfaces         []string `json:"interfaces"`
	Errors             []string `json:"errors"`
	Queries            uint64   `json:"queries"`
	Responses          uint64   `json:"responses"`
	Dropped            uint64   `json:"dropped"`
	Records            int      `json:"records"`
	IgnoredQueries     uint64   `json:"ignored_queries"`
	MalformedResponses uint64   `json:"malformed_responses"`
	SendErrors         uint64   `json:"send_errors"`
	ReceiveErrors      uint64   `json:"receive_errors"`
	CapacityDrops      uint64   `json:"capacity_drops"`
	QuestionDeferrals  uint64   `json:"question_deferrals"`
	LastMalformed      string   `json:"last_malformed,omitempty"`
}

func (m *Manager) Diagnostics() DiscoveryDiagnostics {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.diagnostics
	d.Interfaces = slices.Clone(d.Interfaces)
	d.Errors = slices.Clone(d.Errors)
	if m.persistenceError != "" {
		d.Errors = append(d.Errors, m.persistenceError)
	}
	return d
}

type mdnsQuestion struct {
	name string
	kind uint16
}
type questionState struct {
	next    time.Time
	attempt int
}

func (q mdnsQuestion) reverse() bool {
	return q.kind == 12 && (strings.HasSuffix(q.name, ".in-addr.arpa") || strings.HasSuffix(q.name, ".ip6.arpa"))
}

func (r mdnsRecord) refreshAt() time.Time {
	// RFC 6762 section 5.2: jitter renewal between 80% and 82% of lifetime.
	return r.learned.Add(time.Duration(float64(r.expires.Sub(r.learned)) * (0.80 + rand.Float64()*0.02)))
}

func (m *Manager) runMDNS(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var view *View
	var transport mdnsTransport
	var packets <-chan mdnsDatagram
	var spotify *spotifyDiscovery
	defer func() {
		if spotify != nil {
			spotify.close()
		}
		if transport != nil {
			transport.Close()
		}
	}()
	cache := newMDNSCache()
	observed := map[netip.Addr]time.Time{}
	questions := map[mdnsQuestion]questionState{}
	reverseCount := 0
	preferServices := true
	types, instances := map[string]bool{}, map[string]bool{}
	diag := DiscoveryDiagnostics{Interfaces: []string{}, Errors: []string{}}
	lastPublish := time.Time{}
	nextEnumeration := time.Time{}
	retryOpen := time.Time{}
	report := func() { m.mu.Lock(); m.diagnostics = diag; m.mu.Unlock() }
	add := func(q mdnsQuestion, now time.Time) {
		if _, ok := questions[q]; ok {
			return
		}
		// Independent capacity and alternating dispatch prevent address churn
		// from starving the PTR → SRV/TXT → A/AAAA service resolution chain.
		if (q.reverse() && reverseCount >= 32) || (!q.reverse() && len(questions)-reverseCount >= 96) {
			diag.QuestionDeferrals++
			return
		}
		questions[q] = questionState{next: now}
		if q.reverse() {
			reverseCount++
		}
	}
	for {
		now := time.Now()
		current := m.current()
		if current != view {
			if spotify != nil {
				spotify.close()
				spotify = nil
			}
			if transport != nil {
				transport.Close()
				transport = nil
				packets = nil
			}
			view = current
			cache = newMDNSCache()
			questions = map[mdnsQuestion]questionState{}
			reverseCount = 0
			preferServices = true
			types, instances = map[string]bool{}, map[string]bool{}
			retryOpen = time.Time{}
			nextEnumeration = time.Time{}
			diag = DiscoveryDiagnostics{Enabled: view != nil && view.settings.MDNS.Enabled, Interfaces: []string{}, Errors: []string{}}
			if diag.Enabled {
				spotify = newSpotifyDiscovery(ctx, m.lookupSpotify)
			}
			m.mu.Lock()
			for a, old := range m.mdnsNames {
				if !compatibleNames(old.view, view) || !retainDiscovered(old.name, now) || !diag.Enabled {
					delete(m.mdnsNames, a)
					continue
				}
				old.view = view
				m.mdnsNames[a] = old
				if len(observed) < 4096 || !observed[a].IsZero() {
					observed[a] = now
				}
			}
			m.mu.Unlock()
			report()
		}
		if diag.Enabled && transport == nil && !now.Before(retryOpen) {
			transport, diag.Errors = m.openMDNS(ctx, view.settings.MDNS)
			retryOpen = now.Add(time.Minute)
			if transport != nil {
				packets = transport.Packets()
				diag.Running = true
				for _, id := range transport.Interfaces() {
					if i, e := net.InterfaceByIndex(id); e == nil {
						diag.Interfaces = append(diag.Interfaces, i.Name)
					}
				}
			}
			report()
		}
		select {
		case <-ctx.Done():
			return
		case a := <-m.mdnsObserve:
			if !diag.Enabled {
				continue
			}
			if len(observed) < 4096 || !observed[a].IsZero() {
				observed[a] = now
			} else {
				diag.Dropped++
				diag.CapacityDrops++
			}
		case p := <-packets:
			now = time.Now()
			if p.err != nil {
				diag.ReceiveErrors++
				diag.Dropped++
				transport.Close()
				transport = nil
				packets = nil
				diag.Running = false
				diag.Interfaces = []string{}
				retryOpen = now.Add(time.Minute)
				diag.Errors = append(diag.Errors, p.err.Error())
				if len(diag.Errors) > 16 {
					diag.Errors = diag.Errors[len(diag.Errors)-16:]
				}
				report()
				continue
			}
			if h, err := dnswire.ParseHeader(p.wire); err == nil && h.Flags&0x8000 == 0 {
				// Multicast sockets also receive peers' queries and our loopback
				// traffic. Neither is a malformed response or a discovery failure.
				diag.IgnoredQueries++
				continue
			}
			if err := cache.ingest(p.iface, p.wire, now); err != nil {
				diag.Dropped++
				diag.MalformedResponses++
				diag.LastMalformed = err.Error()
			} else {
				diag.Responses++
				// A new answer may shorten a TTL or arrive during retry cooldown.
				// Bring its existing deadline forward immediately; do not wait for
				// the old deadline to discover that the new record already expired.
				cache.index()
				for q, state := range questions {
					for _, id := range transport.Interfaces() {
						for _, r := range cache.owners[recordGroup{id, q.name, q.kind}] {
							if !r.learned.Equal(now) {
								continue
							}
							if at := r.refreshAt(); at.Before(state.next) {
								state.next = at
							}
							state.attempt = 0
						}
					}
					questions[q] = state
				}
			}
		case now = <-ticker.C:
			if transport == nil {
				continue
			}
			cache.expire(now)
			for q, s := range questions {
				if s.attempt >= 3 && !now.Before(s.next) {
					delete(questions, q)
					if q.reverse() {
						reverseCount--
					}
				}
			}
			// Enumeration remains an active question, including its TTL renewal.
			// Reserve its place before the bounded per-address work queue fills.
			add(mdnsQuestion{"_services._dns-sd._udp.local", 12}, now)
			for a, last := range observed {
				if now.Sub(last) > time.Hour {
					delete(observed, a)
				} else {
					add(mdnsQuestion{localdns.Reverse(a), 12}, now)
				}
			}
			if !now.Before(nextEnumeration) {
				types, instances = map[string]bool{}, map[string]bool{}
				nextEnumeration = now.Add(5 * time.Minute)
			}
			// Discover follow-up questions from validated cached records. The question
			// and graph bounds keep an enthusiastic advertiser from growing work.
			for _, r := range cache.records {
				switch r.key.kind {
				case 12:
					if r.key.owner == "_services._dns-sd._udp.local" {
						if (len(types) < 32 || types[r.target]) && (strings.HasSuffix(r.target, "._tcp.local") || strings.HasSuffix(r.target, "._udp.local")) {
							types[r.target] = true
							add(mdnsQuestion{r.target, 12}, now)
						}
					} else if strings.HasSuffix(r.target, ".local") {
						if strings.Contains(r.target, "._tcp.") || strings.Contains(r.target, "._udp.") {
							if len(instances) < 256 || instances[r.target] {
								instances[r.target] = true
								add(mdnsQuestion{r.target, 33}, now)
								add(mdnsQuestion{r.target, 16}, now)
							}
						} else {
							add(mdnsQuestion{r.target, 1}, now)
							add(mdnsQuestion{r.target, 28}, now)
						}
					}
				case 33:
					add(mdnsQuestion{r.target, 1}, now)
					add(mdnsQuestion{r.target, 28}, now)
				}
			}
			// One question per 500ms tick, sent on each selected interface. IPv4 and
			// IPv6 share a question, with two datagrams per interface per tick.
			var chosen mdnsQuestion
			var earliest time.Time
			for q, s := range questions {
				preferred := q.reverse() != preferServices
				chosenPreferred := chosen.reverse() != preferServices
				if !now.Before(s.next) && (earliest.IsZero() || (preferred && !chosenPreferred) || (preferred == chosenPreferred && s.next.Before(earliest))) {
					chosen, earliest = q, s.next
				}
			}
			if !earliest.IsZero() {
				preferServices = !preferServices
				state := questions[chosen]
				answered := true
				var refresh time.Time
				wire := encodeMDNSQuery(chosen.name, chosen.kind)
				cache.index()
				for _, id := range transport.Interfaces() {
					var due time.Time
					for _, r := range cache.owners[recordGroup{id, chosen.name, chosen.kind}] {
						at := r.refreshAt()
						if due.IsZero() || at.Before(due) {
							due = at
						}
					}
					if now.Before(due) {
						if refresh.IsZero() || due.Before(refresh) {
							refresh = due
						}
						continue
					}
					answered = false
					if err := transport.Send(id, wire); err != nil {
						diag.Dropped++
						diag.SendErrors++
						message := fmt.Sprintf("Interface %d: %v", id, err)
						if !slices.Contains(diag.Errors, message) {
							diag.Errors = append(diag.Errors, message)
							if len(diag.Errors) > 16 {
								diag.Errors = diag.Errors[len(diag.Errors)-16:]
							}
						}
					} else {
						diag.Queries++
					}
				}
				state.attempt++
				if answered {
					state.next = refresh
					state.attempt = 3
				} else if state.attempt >= 3 {
					state.next = now.Add(time.Minute)
					state.attempt = 3
				} else {
					state.next = now.Add(time.Duration(1<<state.attempt) * time.Second)
				}
				if !refresh.IsZero() && refresh.Before(state.next) {
					state.next = refresh
				}
				questions[chosen] = state
			}
			if now.Sub(lastPublish) >= time.Second {
				names := make(map[netip.Addr]entry, len(observed))
				for a := range observed {
					n := cache.lookup(a, now)
					if spotify != nil {
						n = spotify.apply(n, now)
					}
					n = enrichDiscovered(n, now)
					if n.Name != "" {
						names[a] = entry{view: view, name: n}
					}
				}
				m.mu.Lock()
				if m.current() == view {
					// A missing response must not erase the last known identity.
					// Partial expiry can also remove useful metadata while leaving
					// a generic hostname. Preserve that identity until confirmation
					// of a replacement or the original retention deadline.
					for a, old := range m.mdnsNames {
						live, exists := names[a]
						if compatibleNames(old.view, view) && (exists || len(names) < 4096) {
							n := preferDiscovered(old.name, live.name, now)
							if n.Name != "" {
								names[a] = entry{view: view, name: n}
							}
						}
					}
					m.mdnsNames = names
				}
				m.mu.Unlock()
				lastPublish = now
				diag.Records = len(cache.records)
				report()
			}
		}
	}
}
