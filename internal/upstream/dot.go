package upstream

import (
	"context"
	"crypto/tls"
	"net"
	"net/netip"
)

func (c *Client) dialEndpoint(ctx context.Context, e Endpoint) (net.Conn, error) {
	addresses, err := c.bootstrapAddresses(ctx, e)
	if err != nil {
		return nil, err
	}
	var last error
	for _, a := range addresses {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", netip.AddrPortFrom(a, e.port).String())
		if err != nil {
			last = err
			continue
		}
		if e.transport == "dot" {
			secure := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: e.host, RootCAs: c.options.RootCAs})
			if err = secure.HandshakeContext(ctx); err != nil {
				conn.Close()
				last = err
				continue
			}
			conn = secure
		}
		return conn, nil
	}
	return nil, last
}
