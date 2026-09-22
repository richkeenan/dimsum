package upstream_test

import (
	"context"
	"encoding/binary"
	"github.com/richkeenan/dimsum/internal/upstream"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tcpQuery() []byte {
	// A padded query forces TCP without a preceding UDP exchange.
	wire := query()
	wire[11] = 1
	wire = append(wire, 0, 0, 41, 4, 208, 0, 0, 0, 0, 4, 180, 0, 12, 4, 176)
	wire = append(wire, make([]byte, 1200)...)
	return wire
}

func TestTCPReuseAndClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	wire := tcpQuery()
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
		assert.Equal(t, upstream.PlainEndpoint(netip.MustParseAddrPort(ln.Addr().String())), r.Endpoint)
	}
	require.NoError(t, c.Close())
	require.NoError(t, <-done)
	_, err = c.Exchange(context.Background(), query(), make([]byte, 65535))
	assert.ErrorIs(t, err, net.ErrClosed)
}

func TestTCPRepeatedDisconnectAndLateCancellation(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	const cycles = 30
	done := make(chan error, 1)
	go func() {
		for range cycles {
			conn, e := ln.Accept()
			if e != nil {
				done <- e
				return
			}
			e = func() error {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(time.Second))
				var prefix [2]byte
				if _, e := io.ReadFull(conn, prefix[:]); e != nil {
					return e
				}
				p := make([]byte, int(binary.BigEndian.Uint16(prefix[:])))
				if _, e := io.ReadFull(conn, p); e != nil {
					return e
				}
				_, e := conn.Write(append(prefix[:], answer(p)...))
				return e
			}()
			if e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	c := client(t, ln.Addr().String(), time.Second)
	wire, out := tcpQuery(), make([]byte, 65535)
	for range cycles {
		ctx, cancel := context.WithCancel(context.Background())
		_, err = c.Exchange(ctx, wire, out)
		cancel()
		require.NoError(t, err)
		_, err = c.Exchange(context.Background(), wire, out)
		assert.Error(t, err, "EOF on retired peer must discard reused connection")
		assert.Zero(t, c.Outstanding())
	}
	require.NoError(t, <-done)
	assert.Equal(t, uint64(cycles), c.Health()[0].Responses)
	assert.Equal(t, uint64(cycles), c.Health()[0].Failures)
}
