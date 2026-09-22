package upstream

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/netip"
	"time"
)

func (c *Client) dialEndpoint(ctx context.Context, e Endpoint) (net.Conn, error) {
	addresses, err := c.bootstrapAddresses(ctx, e)
	if err != nil {
		return nil, fmt.Errorf("upstream bootstrap for %s: %w", e.host, err)
	}
	var last error = ErrResponse
	for i, a := range addresses {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// A literal dial has no built-in multi-address fallback. Apportion the
		// remaining budget across candidates, including their TLS handshakes.
		budget := c.options.AttemptTimeout
		if deadline, ok := ctx.Deadline(); ok {
			budget = time.Until(deadline)
		}
		candidate, cancel := context.WithTimeout(ctx, budget/time.Duration(len(addresses)-i))
		conn, err := (&net.Dialer{}).DialContext(candidate, "tcp", netip.AddrPortFrom(a, e.port).String())
		if err != nil {
			cancel()
			last = err
			continue
		}
		if e.transport == "dot" || e.transport == "doh" {
			config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: e.host, RootCAs: c.options.RootCAs}
			if e.transport == "doh" {
				config.NextProtos = []string{"h2", "http/1.1"}
				conn, err = c.connections.track(conn)
				if err != nil {
					cancel()
					return nil, err
				}
			}
			secure := tls.Client(conn, config)
			if err = secure.HandshakeContext(candidate); err != nil {
				conn.Close()
				cancel()
				last = err
				continue
			}
			conn = secure
		}
		cancel()
		return conn, nil
	}
	return nil, last
}
