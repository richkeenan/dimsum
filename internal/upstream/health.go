package upstream

import (
	"net/netip"
	"time"
)

type Health struct {
	Endpoint                               netip.AddrPort
	Fallback                               bool
	State                                  string
	RetryAt                                time.Time
	Latency                                time.Duration
	Responses, Failures, SERVFAIL, REFUSED uint64
}
type endpointHealth struct {
	Health
	consecutive int
	backoff     time.Duration
	probe       bool
	epoch       uint64
}

// Health returns owned metric values; no mutable state escapes the client lock.
func (c *Client) Health() []Health {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]Health, len(c.health))
	for i, h := range c.health {
		result[i] = h.Health
		result[i].Endpoint = c.endpoint(i)
		result[i].Fallback = i >= len(c.options.Endpoints)
		result[i].State = "closed"
		if !h.RetryAt.IsZero() {
			result[i].State = "open"
		}
		if h.probe {
			result[i].State = "half-open"
		}
	}
	return result
}
func (c *Client) claim(i int) (bool, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := &c.health[i]
	if h.RetryAt.IsZero() {
		return true, h.epoch
	}
	if h.probe || time.Now().Before(h.RetryAt) {
		return false, 0
	}
	h.probe = true
	h.epoch++
	return true, h.epoch
}
func (c *Client) record(i int, epoch uint64, elapsed time.Duration, err error, code uint16, canceled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := &c.health[i]
	if epoch != h.epoch {
		return
	} // A late completion cannot change a newer circuit/probe.
	probe := h.probe
	h.probe = false
	if canceled {
		return
	}
	if err == nil {
		h.Responses++
		h.consecutive = 0
		if h.Latency == 0 {
			h.Latency = elapsed
		} else {
			h.Latency = (h.Latency*7 + elapsed) / 8
		}
		if code == 2 {
			h.SERVFAIL++
		}
		if code == 5 {
			h.REFUSED++
		}
		if !probe || (code != 2 && code != 5) {
			h.RetryAt = time.Time{}
			h.backoff = 0
			return
		}
	} else {
		h.Failures++
		h.consecutive++
	}
	if probe || h.consecutive >= c.options.FailureThreshold {
		h.epoch++
		if h.backoff == 0 {
			h.backoff = c.options.OpenInterval
		} else {
			h.backoff = min(h.backoff*2, c.options.MaxBackoff)
		}
		// Positive jitter never probes earlier than the configured interval.
		jitter := time.Duration(0)
		if n, e := random16(); e == nil {
			jitter = time.Duration(uint64(h.backoff/10) * uint64(n) / 65535)
		}
		h.RetryAt = time.Now().Add(min(h.backoff+jitter, c.options.MaxBackoff))
	}
}
