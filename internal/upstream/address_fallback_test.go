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
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncryptedAddressFallbackVerifiesEachCandidate(t *testing.T) {
	for _, scheme := range []string{"tls", "https"} {
		for _, failure := range []string{"stall", "invalid-tls"} {
			t.Run(scheme+"/"+failure, func(t *testing.T) {
				first, err := net.Listen("tcp6", "[::1]:0")
				require.NoError(t, err)
				defer first.Close()
				port := first.Addr().(*net.TCPAddr).Port
				good, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
				require.NoError(t, err)
				defer good.Close()
				firstClosed := make(chan struct{})
				go func() {
					defer close(firstClosed)
					conn, err := first.Accept()
					if err != nil {
						return
					}
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
					if failure == "invalid-tls" {
						_, _ = conn.Write([]byte("not a TLS response"))
					}
					_, _ = io.Copy(io.Discard, conn)
				}()
				cert := httptest.NewTLSServer(http.NotFoundHandler())
				defer cert.Close()
				roots := x509.NewCertPool()
				roots.AddCert(cert.Certificate())
				if scheme == "https" {
					server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						b, err := io.ReadAll(r.Body)
						if err != nil {
							return
						}
						b[2] |= 0x80
						w.Header().Set("Content-Type", "application/dns-message")
						_, _ = w.Write(b)
					}))
					server.Listener.Close()
					server.Listener = good
					server.TLS = cert.TLS.Clone()
					server.EnableHTTP2 = true
					server.StartTLS()
					defer server.Close()
				} else {
					listener := tls.NewListener(good, cert.TLS.Clone())
					go func() {
						conn, err := listener.Accept()
						if err != nil {
							return
						}
						defer conn.Close()
						_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
						var prefix [2]byte
						if _, err = io.ReadFull(conn, prefix[:]); err != nil {
							return
						}
						b := make([]byte, binary.BigEndian.Uint16(prefix[:]))
						if _, err = io.ReadFull(conn, b); err != nil {
							return
						}
						b[2] |= 0x80
						_, _ = conn.Write(append(prefix[:], b...))
					}()
				}
				address := scheme + "://" + net.JoinHostPort("example.com", strconv.Itoa(port))
				if scheme == "https" {
					address += "/dns-query"
				}
				c := encryptedClient(t, address, roots)
				defer c.Close()
				c.options.Timeout = 800 * time.Millisecond
				c.options.AttemptTimeout = 800 * time.Millisecond
				c.bootstrap.entries = map[string]bootstrapEntry{"example.com": {addresses: []netip.Addr{netip.MustParseAddr("::1"), netip.MustParseAddr("127.0.0.1")}, expires: time.Now().Add(time.Minute)}}
				start := time.Now()
				r, err := c.Exchange(context.Background(), encryptedQuery(), make([]byte, 65535))
				require.NoError(t, err)
				assert.Greater(t, r.N, 12)
				assert.Less(t, time.Since(start), 800*time.Millisecond)
				select {
				case <-firstClosed:
				case <-time.After(100 * time.Millisecond):
					assert.Fail(t, "failed candidate socket remains open")
				}
			})
		}
	}
}
