package upstream

import (
	"context"
	"net/netip"
)

// LookupIP resolves service hostnames through the configured pool, including its
// encryption policy. It never uses the OS resolver or the bootstrap-only path.
func (c *Client) LookupIP(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a}, nil
	}
	e, err := ParseEndpoint("tls://" + host)
	if err != nil || e.host != host {
		return nil, ErrResponse
	}
	ctx, cancel := context.WithTimeout(ctx, c.options.Timeout)
	defer cancel()
	// A one-slot configuration must still resolve both families rather than
	// rejecting whichever family loses the local admission race.
	serial := make(chan struct{}, 1)
	addresses, _, err := lookupAddresses(ctx, host, func(ctx context.Context, q, out []byte) (int, error) {
		if c.options.MaxOutstanding == 1 {
			select {
			case serial <- struct{}{}:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
			defer func() { <-serial }()
		}
		r, err := c.Exchange(ctx, q, out)
		return r.N, err
	})
	return addresses, err
}
