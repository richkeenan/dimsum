package upstream

import (
	"context"
	"encoding/binary"
	"io"
	"net/netip"
	"time"

	"github.com/richkeenan/dimsum/internal/dnswire"
)

func (c *Client) exchangeTCP(ctx context.Context, endpoint netip.AddrPort, query []byte, q *dnswire.Question, id uint16, out []byte) (n int, m dnswire.Message, err error) {
	conn, err := c.connections.take(ctx, endpoint)
	if err != nil {
		return 0, dnswire.Message{}, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer func() {
		// If cancellation has started, never return the socket to another lease.
		stopped := stop()
		c.connections.put(endpoint, conn, err == nil && stopped && ctx.Err() == nil)
	}()
	deadline, _ := ctx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return 0, dnswire.Message{}, err
	}
	var prefix [2]byte
	binary.BigEndian.PutUint16(prefix[:], uint16(len(query)))
	for _, p := range [][]byte{prefix[:], query} {
		for len(p) > 0 {
			n, e := conn.Write(p)
			if e != nil {
				return 0, dnswire.Message{}, e
			}
			if n == 0 {
				return 0, dnswire.Message{}, io.ErrShortWrite
			}
			p = p[n:]
		}
	}
	if _, err = io.ReadFull(conn, prefix[:]); err != nil {
		return 0, dnswire.Message{}, err
	}
	n = int(binary.BigEndian.Uint16(prefix[:]))
	if n < 12 {
		return 0, dnswire.Message{}, ErrResponse
	}
	if _, err = io.ReadFull(conn, out[:n]); err != nil {
		return 0, dnswire.Message{}, err
	}
	if ctx.Err() != nil || !time.Now().Before(deadline) {
		return 0, dnswire.Message{}, context.DeadlineExceeded
	}
	m, err = validate(out[:n], q, id)
	return n, m, err
}
