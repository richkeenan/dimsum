package upstream_test

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTCPReuseAndClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	// A padded query forces TCP without a preceding UDP exchange.
	wire := query()
	wire[11] = 1
	wire = append(wire, 0, 0, 41, 4, 208, 0, 0, 0, 0, 4, 180, 0, 12, 4, 176)
	wire = append(wire, make([]byte, 1200)...)
	done := make(chan error, 1)
	go func() {
		conn, e := ln.Accept()
		if e != nil {
			done <- e
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		for range 20 {
			var prefix [2]byte
			if _, e = io.ReadFull(conn, prefix[:]); e != nil {
				done <- e
				return
			}
			p := make([]byte, int(binary.BigEndian.Uint16(prefix[:])))
			if _, e = io.ReadFull(conn, p); e != nil {
				done <- e
				return
			}
			p = answer(p)
			if _, e = conn.Write(append(prefix[:], p...)); e != nil {
				done <- e
				return
			}
		}
		var b [1]byte
		_, e = conn.Read(b[:])
		if e == io.EOF {
			e = nil
		}
		done <- e
	}()
	c := client(t, ln.Addr().String(), time.Second)
	defer c.Close()
	for range 20 {
		r, e := c.Exchange(context.Background(), wire, make([]byte, 65535))
		require.NoError(t, e)
		assert.True(t, r.TCP)
		assert.Equal(t, netip.MustParseAddrPort(ln.Addr().String()), r.Endpoint)
	}
	require.NoError(t, c.Close())
	require.NoError(t, <-done)
	_, err = c.Exchange(context.Background(), query(), make([]byte, 65535))
	assert.ErrorIs(t, err, net.ErrClosed)
}
