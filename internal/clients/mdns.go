package clients

import (
	"context"
	"fmt"
	"github.com/richkeenan/dimsum/internal/localdns"
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
	Enabled    bool     `json:"enabled"`
	Running    bool     `json:"running"`
	Interfaces []string `json:"interfaces"`
	Errors     []string `json:"errors"`
	Queries    uint64   `json:"queries"`
	Responses  uint64   `json:"responses"`
	Dropped    uint64   `json:"dropped"`
	Records    int      `json:"records"`
}

func (m *Manager) Diagnostics() DiscoveryDiagnostics {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.diagnostics
	d.Interfaces = slices.Clone(d.Interfaces)
	d.Errors = slices.Clone(d.Errors)
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

func (m *Manager) runMDNS(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var view *View
	var transport mdnsTransport
	var packets <-chan mdnsDatagram
	defer func() {
		if transport != nil {
			transport.Close()
		}
	}()
	cache := newMDNSCache()
	observed := map[netip.Addr]time.Time{}
	questions := map[mdnsQuestion]questionState{}
	diag := DiscoveryDiagnostics{Interfaces: []string{}, Errors: []string{}}
	lastPublish := time.Time{}
	nextEnumeration := time.Time{}
	retryOpen := time.Time{}
	report := func() { m.mu.Lock(); m.diagnostics = diag; m.mu.Unlock() }
	add := func(q mdnsQuestion, now time.Time) {
		if _, ok := questions[q]; !ok && len(questions) < 128 {
			questions[q] = questionState{next: now}
		}
	}
	for {
		now := time.Now()
		current := m.current()
		if current != view {
			if transport != nil {
				transport.Close()
				transport = nil
				packets = nil
			}
			view = current
			cache = newMDNSCache()
			questions = map[mdnsQuestion]questionState{}
			retryOpen = time.Time{}
			nextEnumeration = time.Time{}
			diag = DiscoveryDiagnostics{Enabled: view != nil && view.settings.MDNS.Enabled, Interfaces: []string{}, Errors: []string{}}
			m.mu.Lock()
			m.mdnsNames = make(map[netip.Addr]entry)
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
				add(mdnsQuestion{localdns.Reverse(a), 12}, now)
			} else {
				diag.Dropped++
			}
		case p := <-packets:
			if p.err != nil {
				diag.Errors = append(diag.Errors, p.err.Error())
				if len(diag.Errors) > 16 {
					diag.Errors = diag.Errors[len(diag.Errors)-16:]
				}
				report()
				continue
			}
			if err := cache.ingest(p.iface, p.wire, now); err != nil {
				diag.Dropped++
			} else {
				diag.Responses++
			}
		case <-ticker.C:
			if transport == nil {
				continue
			}
			cache.expire(now)
			for q, s := range questions {
				if s.attempt == 0 && !now.Before(s.next) {
					delete(questions, q)
				}
			}
			for a, last := range observed {
				if now.Sub(last) > time.Hour {
					delete(observed, a)
				} else {
					add(mdnsQuestion{localdns.Reverse(a), 12}, now)
				}
			}
			if !now.Before(nextEnumeration) {
				add(mdnsQuestion{"_services._dns-sd._udp.local", 12}, now)
				nextEnumeration = now.Add(5 * time.Minute)
			}
			// Discover follow-up questions from validated cached records. The question
			// and graph bounds keep an enthusiastic advertiser from growing work.
			types, instances := map[string]bool{}, map[string]bool{}
			for _, r := range cache.records {
				switch r.key.kind {
				case 12:
					if r.key.owner == "_services._dns-sd._udp.local" && len(types) < 32 && (strings.HasSuffix(r.target, "._tcp.local") || strings.HasSuffix(r.target, "._udp.local")) {
						types[r.target] = true
						add(mdnsQuestion{r.target, 12}, now)
					} else if strings.HasSuffix(r.target, ".local") {
						if strings.Contains(r.target, "._tcp.") || strings.Contains(r.target, "._udp.") {
							if len(instances) < 256 {
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
			// One question per 250ms tick, sent on each selected interface. IPv4 and
			// IPv6 share a question, with two datagrams per tick at most.
			var chosen mdnsQuestion
			var earliest time.Time
			for q, s := range questions {
				if !now.Before(s.next) && (earliest.IsZero() || s.next.Before(earliest)) {
					chosen, earliest = q, s.next
				}
			}
			if !earliest.IsZero() {
				state := questions[chosen]
				answered := true
				wire := encodeMDNSQuery(chosen.name, chosen.kind)
				for _, id := range transport.Interfaces() {
					hasAnswer := false
					for _, r := range cache.records {
						if r.key.iface == id && r.key.owner == chosen.name && r.key.kind == chosen.kind && r.expires.Sub(now) > 30*time.Second {
							hasAnswer = true
							break
						}
					}
					if hasAnswer {
						continue
					}
					answered = false
					if err := transport.Send(id, wire); err != nil {
						diag.Dropped++
					} else {
						diag.Queries++
					}
				}
				state.attempt++
				if state.attempt >= 3 || answered {
					state.next = now.Add(time.Minute)
					state.attempt = 0
				} else {
					state.next = now.Add(time.Duration(1<<state.attempt) * time.Second)
				}
				questions[chosen] = state
			}
			if now.Sub(lastPublish) >= time.Second {
				names := make(map[netip.Addr]entry, len(observed))
				for a := range observed {
					n := enrichDiscovered(cache.lookup(a, now), now)
					if n.Name != "" {
						names[a] = entry{view: view, name: n}
					}
				}
				m.mu.Lock()
				if m.current() == view {
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
