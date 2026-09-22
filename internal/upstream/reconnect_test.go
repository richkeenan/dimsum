package upstream

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoTRetiredConnectionReconnect(t *testing.T) {
	for _, mode := range []string{"recover", "budget", "cancel", "deadline", "fresh-failure", "verify-again", "invalid-reply"} {
		t.Run(mode, func(t *testing.T) {
			cert := httptest.NewTLSServer(http.NotFoundHandler())
			defer cert.Close()
			roots := x509.NewCertPool()
			roots.AddCert(cert.Certificate())
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer ln.Close()
			closed := make(chan struct{})
			done := make(chan error, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				raw, e := ln.Accept()
				if e != nil {
					done <- e
					return
				}
				conn := tls.Server(raw, cert.TLS.Clone())
				conn.SetDeadline(time.Now().Add(3 * time.Second))
				responses := 0
				respond := func(c net.Conn) error {
					var size [2]byte
					if _, e := io.ReadFull(c, size[:]); e != nil {
						return e
					}
					b := make([]byte, binary.BigEndian.Uint16(size[:]))
					if _, e := io.ReadFull(c, b); e != nil {
						return e
					}
					b[2] |= 0x80
					responses++
					if mode == "invalid-reply" && responses == 2 {
						b[0] ^= 1
					}
					_, e := c.Write(append(size[:], b...))
					return e
				}
				e = respond(conn)
				if e == nil && mode == "invalid-reply" {
					close(closed)
					done <- respond(conn)
					conn.Close()
					return
				}
				conn.Close()
				close(closed)
				if e != nil || mode == "budget" {
					done <- e
					return
				}
				raw, e = ln.Accept()
				if e != nil {
					done <- e
					return
				}
				defer raw.Close()
				if mode == "cancel" || mode == "deadline" {
					if mode == "cancel" {
						cancel()
					}
					raw.SetDeadline(time.Now().Add(3 * time.Second))
					_, _ = io.Copy(io.Discard, raw)
					done <- nil
					return
				}
				conn = tls.Server(raw, cert.TLS.Clone())
				conn.SetDeadline(time.Now().Add(3 * time.Second))
				if mode == "fresh-failure" || mode == "verify-again" {
					e := conn.Handshake()
					if mode == "verify-again" {
						e = nil
					} // client rejects the now-untrusted peer
					done <- e
					return
				}
				done <- respond(conn)
			}()
			c := encryptedClient(t, "tls://"+ln.Addr().String(), roots)
			c.options.Timeout = 500 * time.Millisecond
			if mode == "budget" {
				c.options.MaxAttempts = 1
			}
			_, err = c.Exchange(context.Background(), encryptedQuery(), make([]byte, 65535))
			require.NoError(t, err)
			<-closed
			if mode == "verify-again" {
				c.options.RootCAs = x509.NewCertPool()
			}
			started := time.Now()
			r, err := c.Exchange(ctx, encryptedQuery(), make([]byte, 65535))
			if mode == "recover" {
				assert.NoError(t, err)
				assert.Equal(t, "dot", r.Transport)
			} else {
				assert.Error(t, err)
			}
			if mode == "budget" || mode == "invalid-reply" {
				assert.Equal(t, 1, r.Attempts)
			} else {
				assert.Equal(t, 2, r.Attempts)
			}
			if mode == "cancel" {
				assert.ErrorIs(t, err, context.Canceled)
			}
			if mode == "deadline" {
				assert.ErrorIs(t, err, context.DeadlineExceeded)
				assert.Less(t, time.Since(started), time.Second)
			}
			if mode == "verify-again" {
				var untrusted x509.UnknownAuthorityError
				assert.ErrorAs(t, err, &untrusted)
			}
			ln.Close()
			require.NoError(t, <-done)
			assert.Zero(t, c.Outstanding())
		})
	}
}
