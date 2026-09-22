package upstream

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/richkeenan/dimsum/internal/dnswire"
)

type bootstrapEntry struct {
	addresses []netip.Addr
	expires   time.Time
}
type bootstrapCache struct {
	mu      sync.Mutex
	entries map[string]bootstrapEntry
	flights map[string]chan struct{}
}

func (c *Client) bootstrapAddresses(ctx context.Context, e Endpoint) ([]netip.Addr, error) {
	if e.literal.IsValid() {
		return []netip.Addr{e.literal}, nil
	}
	key := strings.ToLower(strings.TrimSuffix(e.host, "."))
	b := &c.bootstrap
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		b.mu.Lock()
		if v, ok := b.entries[key]; ok && time.Now().Before(v.expires) {
			b.mu.Unlock()
			return append([]netip.Addr(nil), v.addresses...), nil
		}
		if wait := b.flights[key]; wait != nil {
			b.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-wait:
				continue
			}
		}
		if b.entries == nil {
			b.entries = make(map[string]bootstrapEntry)
			b.flights = make(map[string]chan struct{})
		}
		wait := make(chan struct{})
		b.flights[key] = wait
		b.mu.Unlock()
		started := time.Now()
		addresses, ttl, err := lookupAddresses(ctx, key, c.bootstrapExchange)
		ttl -= time.Since(started) // Never extend the earliest answer TTL while waiting for the other family.
		b.mu.Lock()
		if err == nil && ttl > 0 {
			if len(b.entries) >= 32 {
				var oldest string
				var expiry time.Time
				for k, v := range b.entries {
					if expiry.IsZero() || v.expires.Before(expiry) {
						oldest = k
						expiry = v.expires
					}
				}
				delete(b.entries, oldest)
			}
			b.entries[key] = bootstrapEntry{append([]netip.Addr(nil), addresses...), time.Now().Add(min(ttl, time.Hour))}
		}
		delete(b.flights, key)
		close(wait)
		b.mu.Unlock()
		return addresses, err
	}
}

// Bootstrap traffic contains only encrypted endpoint hostnames, never client questions.
func (c *Client) bootstrapExchange(ctx context.Context, wire, out []byte) (int, error) {
	query, q, err := prepare(wire)
	if err != nil {
		return 0, err
	}
	id, err := c.ids.acquire()
	if err != nil {
		return 0, err
	}
	defer c.ids.release(id, max(4*time.Second, 2*c.options.Timeout))
	binary.BigEndian.PutUint16(query, id)
	var last error = ErrResponse
	for i, a := range c.options.BootstrapDNS {
		deadline, ok := ctx.Deadline()
		if !ok {
			deadline = time.Now().Add(c.options.AttemptTimeout)
		}
		attempt, cancel := context.WithTimeout(ctx, time.Until(deadline)/time.Duration(len(c.options.BootstrapDNS)-i))
		n, m, err := exchangeUDP(attempt, a, query, &q, id, out)
		if err == nil && m.Question.Header.Flags&dnswire.FlagTC != 0 {
			n, m, err = c.exchangeTCP(attempt, PlainEndpoint(a), query, &q, id, out, false)
		}
		cancel()
		if err == nil && m.RCode == 0 && m.Question.Header.Flags&dnswire.FlagTC == 0 {
			return n, nil
		}
		if err != nil {
			last = err
		}
	}
	return 0, last
}

type wireExchange func(context.Context, []byte, []byte) (int, error)

// lookupAddresses owns both family requests and waits for all workers before returning.
func lookupAddresses(ctx context.Context, host string, exchange wireExchange) ([]netip.Addr, time.Duration, error) {
	// Discovery may spend at most half the remaining budget, leaving time for
	// dialing, TLS and the DNS exchange. A successful family waits briefly for
	// its sibling, then cancels and joins it rather than losing usable answers.
	budget := 500 * time.Millisecond
	if deadline, ok := ctx.Deadline(); ok {
		budget = time.Until(deadline) / 2
	}
	discovery, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	type result struct {
		addresses []netip.Addr
		ttl       time.Duration
		err       error
	}
	results := make(chan result, 2)
	for _, typ := range []uint16{1, 28} {
		go func(typ uint16) {
			wire := make([]byte, 12)
			wire[2] = 1
			wire[5] = 1
			for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
				wire = append(wire, byte(len(label)))
				wire = append(wire, label...)
			}
			wire = append(wire, 0, byte(typ>>8), byte(typ), 0, 1)
			out := make([]byte, 65535)
			n, err := exchange(discovery, wire, out)
			if err != nil {
				results <- result{err: err}
				return
			}
			addresses, ttl, err := bootstrapAnswer(out[:n], typ)
			results <- result{addresses, ttl, err}
		}(typ)
	}
	var addresses []netip.Addr
	ttl := time.Hour
	var last error = errors.New("upstream: bootstrap returned no addresses")
	var grace *time.Timer
	defer func() {
		if grace != nil {
			grace.Stop()
		}
	}()
	for range 2 {
		r := <-results
		if r.err != nil {
			last = r.err
			continue
		}
		if len(r.addresses) > 0 {
			addresses = append(addresses, r.addresses...)
			ttl = min(ttl, r.ttl)
			if grace == nil {
				grace = time.AfterFunc(50*time.Millisecond, cancel)
			}
		}
	}
	if ctx.Err() != nil {
		return nil, 0, ctx.Err()
	}
	if len(addresses) == 0 {
		return nil, 0, last
	}
	return addresses[:min(len(addresses), 16)], ttl, nil
}

func bootstrapAnswer(wire []byte, typ uint16) ([]netip.Addr, time.Duration, error) {
	var scanner dnswire.Scanner
	if err := scanner.Init(wire); err != nil {
		return nil, 0, err
	}
	var records []dnswire.Record
	var r dnswire.Record
	for scanner.Next(&r) {
		if r.Section == dnswire.Answer && r.Class == 1 {
			if len(records) >= 128 {
				return nil, 0, ErrResponse
			}
			records = append(records, r)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	owner := scanner.Question.Name
	ttl := time.Hour
	for depth := 0; depth < 8; depth++ {
		var addresses []netip.Addr
		var target dnswire.Name
		alias := false
		for _, r := range records {
			if !bytes.Equal(r.Name.Canonical[:r.Name.Length], owner.Canonical[:owner.Length]) {
				continue
			}
			if r.Type == 5 {
				if err := dnswire.DecodeName(wire, r.DataOffset, &target); err != nil {
					return nil, 0, err
				}
				alias = true
				ttl = min(ttl, time.Duration(r.TTL)*time.Second)
			}
			if r.Type == typ {
				a, ok := netip.AddrFromSlice(r.RData)
				if ok && unicast(a) {
					if len(addresses) < 16 {
						addresses = append(addresses, a)
					}
					ttl = min(ttl, time.Duration(r.TTL)*time.Second)
				}
			}
		}
		if len(addresses) > 0 {
			return addresses, ttl, nil
		}
		if !alias {
			break
		}
		owner = target
	}
	return nil, 0, ErrResponse
}
