package upstream

import (
	"context"
	"encoding/binary"
	"io"
	"time"

	"github.com/richkeenan/dimsum/internal/dnswire"
)

type reusedTransportError struct{ error }

func (e *reusedTransportError) Unwrap() error { return e.error }

func (c *Client) exchangeTCP(ctx context.Context, endpoint Endpoint, query []byte, q *dnswire.Question, id uint16, out []byte, fresh bool) (n int, m dnswire.Message, err error) {
	conn, reused, err := c.connections.take(ctx, endpoint, c.dialEndpoint, fresh)
	if err != nil {
		return 0, dnswire.Message{}, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	transportFailure := true
	defer func() {
		// If cancellation has started, never return the socket to another lease.
		stopped := stop()
		c.connections.put(endpoint, conn, err == nil && stopped && ctx.Err() == nil)
		if err != nil && reused && transportFailure && ctx.Err() == nil {
			err = &reusedTransportError{err}
		}
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
		transportFailure = false
		return 0, dnswire.Message{}, ErrResponse
	}
	if _, err = io.ReadFull(conn, out[:n]); err != nil {
		return 0, dnswire.Message{}, err
	}
	transportFailure = false
	if ctx.Err() != nil || !time.Now().Before(deadline) {
		return 0, dnswire.Message{}, context.DeadlineExceeded
	}
	m, err = validate(out[:n], q, id)
	return n, m, err
}
