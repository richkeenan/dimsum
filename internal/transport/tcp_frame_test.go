package transport

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type frameWriteConn struct {
	net.Conn
	limit  int
	writes []int
}

func (c *frameWriteConn) Write(p []byte) (int, error) {
	c.writes = append(c.writes, len(p))
	if c.limit > 0 && len(p) > c.limit {
		p = p[:c.limit]
	}
	return c.Conn.Write(p)
}

// Splitting a DNS frame at Write can produce two tiny TCP segments. The reply
// must be offered as one frame, while still tolerating a short-writing stream.
func TestTCPReplyOffersCompleteFrame(t *testing.T) {
	for _, tc := range []struct {
		name        string
		size, limit int
	}{
		{"small", 32, 0}, {"maximum", 65535, 0}, {"short-writes", 32, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := new(dns.Msg)
			q.SetQuestion("frame.example.", dns.TypeA)
			wire, err := q.Pack()
			require.NoError(t, err)
			reply := new(dns.Msg)
			reply.SetReply(q)
			dataSize := tc.size
			if tc.size == 65535 {
				dataSize = 65535 - len(wire) - 11 // root owner and RR header
			}
			reply.Answer = []dns.RR{&dns.RFC3597{Hdr: dns.RR_Header{Name: ".", Rrtype: 65280, Class: dns.ClassINET, Ttl: 60}, Rdata: strings.Repeat("a5", dataSize)}}
			payload, err := reply.Pack()
			require.NoError(t, err)
			if tc.size == 65535 {
				require.Len(t, payload, 65535)
			}
			s, err := New(Options{}, HandlerFunc(func(_ context.Context, _ *Request, out []byte) (int, error) {
				return copy(out, payload), nil
			}))
			require.NoError(t, err)
			server, client := net.Pipe()
			conn := &frameWriteConn{Conn: server, limit: tc.limit}
			done := make(chan struct{})
			go func() { defer close(done); s.serveConn(context.Background(), conn) }()
			t.Cleanup(func() { _ = client.Close(); _ = server.Close(); <-done })
			require.NoError(t, client.SetDeadline(time.Now().Add(3*time.Second)))
			request := make([]byte, 2+len(wire))
			binary.BigEndian.PutUint16(request, uint16(len(wire)))
			copy(request[2:], wire)
			_, err = client.Write(request)
			require.NoError(t, err)
			frame := make([]byte, 2+len(payload))
			_, err = io.ReadFull(client, frame)
			require.NoError(t, err)
			assert.EqualValues(t, len(payload), binary.BigEndian.Uint16(frame))
			assert.Equal(t, payload, frame[2:])
			require.NoError(t, client.Close())
			<-done
			require.NotEmpty(t, conn.writes)
			assert.Equal(t, len(payload)+2, conn.writes[0], "first write must contain the entire frame")
			if tc.limit == 0 {
				assert.Len(t, conn.writes, 1)
			}
		})
	}
}
