package transport

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"
)

// ServeTCP bounds live connections globally. Each connection processes one
// request at a time, preserving pipeline order and applying socket backpressure
// instead of allocating a pipeline queue. Cancellation closes active clients.
func (s *Server) ServeTCP(ctx context.Context, l net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { l.Close() })
	defer stop()
	var clients sync.WaitGroup
	defer clients.Wait()
	defer cancel()
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil || isClosed(err) {
				return nil
			}
			return err
		}
		select {
		case s.connections <- struct{}{}:
			s.stats.connections.Add(1)
			clients.Go(func() { defer func() { <-s.connections; s.stats.connections.Add(^uint64(0)) }(); s.serveConn(ctx, c) })
		default:
			s.stats.connectionDrops.Add(1)
			c.Close()
		}
	}
}

func (s *Server) serveConn(ctx context.Context, c net.Conn) {
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	peer, _ := netip.ParseAddrPort(c.RemoteAddr().String())
	// Reserve the framing prefix so a normal reply needs one stream write.
	// TCP_NODELAY is already Go's default; this also avoids a tiny prefix segment.
	frame := make([]byte, 2+65535)
	out := frame[2:]
	var prefix [2]byte
	for ctx.Err() == nil {
		c.SetReadDeadline(time.Now().Add(s.opts.ReadTimeout))
		if _, err := io.ReadFull(c, prefix[:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(prefix[:]))
		if n == 0 {
			return
		}
		slot := s.acquire(n)
		if slot == nil {
			return
		}
		slot.wire = slot.wire[:n]
		if _, err := io.ReadFull(c, slot.wire); err != nil {
			s.pool.release(slot)
			return
		}
		size := s.resolve(ctx, slot.wire, out, peer, true, slot.deadline)
		// Output is connection-owned, so the input can be returned before a slow write.
		s.pool.release(slot)
		if size == 0 {
			return
		}
		c.SetWriteDeadline(time.Now().Add(s.opts.WriteTimeout))
		binary.BigEndian.PutUint16(frame[:2], uint16(size))
		if err := writeAll(c, frame[:size+2]); err != nil {
			s.stats.writeErrors.Add(1)
			return
		}
	}
}
func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}
