package upstream

import (
	"net/netip"
	"sort"
)

// RouteKey is the resolution namespace, not a conditional routing rule.
// Private client naming has its own resolver and never enters this pool.
type RouteKey uint64

const DefaultRoute RouteKey = 1

func (c *Client) endpoint(i int) netip.AddrPort {
	if i < len(c.options.Endpoints) {
		return c.options.Endpoints[i]
	}
	return c.options.Fallback[i-len(c.options.Endpoints)]
}

func (c *Client) order() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.selections++
	order := make([]int, len(c.health))
	for i := range order {
		order[i] = i
	}
	if c.options.Mode == "adaptive" {
		// Sort only within a pool: provider fallback never wins over an eligible primary.
		for _, group := range [][]int{order[:len(c.options.Endpoints)], order[len(c.options.Endpoints):]} {
			sort.SliceStable(group, func(i, j int) bool { return c.health[group[i]].Latency < c.health[group[j]].Latency })
			// Deterministic bounded exploration (1/16 selections) also refreshes stale scores.
			if len(group) > 1 && c.selections%16 == 0 {
				i := int(c.selections/16) % len(group)
				group[0], group[i] = group[i], group[0]
			}
		}
	}
	return order
}
