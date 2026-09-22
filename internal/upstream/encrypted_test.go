package upstream

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func encryptedQuery() []byte {
	return []byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 3, 'w', 'w', 'w', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 0, 0, 1, 0, 1}
}
func encryptedClient(t *testing.T, address string, roots *x509.CertPool) *Client {
	t.Helper()
	e, err := ParseEndpoint(address)
	require.NoError(t, err)
	c, err := New(Options{Endpoints: []Endpoint{e}, RootCAs: roots, Timeout: time.Second, AttemptTimeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}
func TestDoHExchangeAndValidation(t *testing.T) {
	for _, mode := range []string{"ok", "status", "media", "oversize", "redirect", "id", "truncated", "encoding", "short", "untrusted", "hostname"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			var protocol atomic.Int32
			s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				protocol.Store(int32(r.ProtoMajor))
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/dns-message" || r.Header.Get("Accept") != "application/dns-message" {
					w.WriteHeader(400)
					return
				}
				b, err := io.ReadAll(r.Body)
				if err != nil {
					return
				}
				b[2] |= 0x80
				w.Header().Set("Content-Type", "application/dns-message")
				switch mode {
				case "status":
					w.WriteHeader(503)
					return
				case "media":
					w.Header().Set("Content-Type", "text/html")
				case "oversize":
					b = make([]byte, 65536)
				case "redirect":
					w.Header().Set("Location", "/other")
					w.WriteHeader(307)
					return
				case "id":
					b[0] ^= 1
				case "truncated":
					b[2] |= 2
				case "encoding":
					w.Header().Set("Content-Encoding", "gzip")
				case "short":
					b = b[:5]
				}
				_, _ = w.Write(b)
			}))
			s.EnableHTTP2 = true
			s.StartTLS()
			defer s.Close()
			roots := x509.NewCertPool()
			roots.AddCert(s.Certificate())
			if mode == "untrusted" {
				roots = x509.NewCertPool()
			}
			address := s.URL + "/dns-query"
			if mode == "hostname" {
				address = strings.Replace(address, "127.0.0.1", "wrong.example", 1)
			}
			c := encryptedClient(t, address, roots)
			if mode == "hostname" {
				c.bootstrap.entries = map[string]bootstrapEntry{"wrong.example": {addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}, expires: time.Now().Add(time.Minute)}}
			}
			out := bytes.Repeat([]byte{0xcc}, 65535)
			result, err := c.Exchange(context.Background(), encryptedQuery(), out)
			if mode == "ok" {
				require.NoError(t, err)
				assert.Equal(t, "doh", result.Transport)
				assert.Equal(t, address, result.Endpoint.String())
				assert.Greater(t, result.N, 12)
				assert.EqualValues(t, 2, protocol.Load(), "HTTP/2 should be explicitly enabled with a custom dialer")
			} else {
				assert.Error(t, err)
				assert.Zero(t, result.N)
				assert.Equal(t, bytes.Repeat([]byte{0xcc}, 65535), out, "invalid replies must not escape to caller storage")
			}
			if mode == "redirect" {
				assert.EqualValues(t, 1, calls.Load())
			}
			if mode == "hostname" {
				var hostnameError x509.HostnameError
				assert.ErrorAs(t, err, &hostnameError)
			}
		})
	}
}

func TestDoHReuseConcurrentRequestsAndNoProxy(t *testing.T) {
	var proxyCalls, connections atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyCalls.Add(1); w.WriteHeader(502) }))
	defer proxy.Close()
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return
		}
		b[2] |= 0x80
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(b)
	}))
	s.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	s.EnableHTTP2 = true
	s.StartTLS()
	defer s.Close()
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	address := strings.Replace(s.URL, "127.0.0.1", "example.com", 1) + "/dns-query"
	c := encryptedClient(t, address, roots)
	c.bootstrap.entries = map[string]bootstrapEntry{"example.com": {addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}, expires: time.Now().Add(time.Minute)}}
	for range 2 {
		_, err := c.Exchange(context.Background(), encryptedQuery(), make([]byte, 65535))
		require.NoError(t, err)
	}
	assert.EqualValues(t, 1, connections.Load())
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Exchange(context.Background(), encryptedQuery(), make([]byte, 65535))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		assert.NoError(t, err)
	}
	assert.Zero(t, proxyCalls.Load())
	assert.EqualValues(t, 1, connections.Load(), "multiplex concurrent exchanges on the established HTTP/2 connection")
}

func TestDoHBootstrapPreservesTLSAndHTTPIdentity(t *testing.T) {
	bootstrap, calls := bootstrapFixture(t, 60, "local")
	observed := make(chan [2]string, 2)
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- [2]string{r.Host, r.TLS.ServerName}
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return
		}
		b[2] |= 0x80
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(b)
	}))
	defer s.Close()
	require.NoError(t, s.Certificate().VerifyHostname("example.com"))
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	address := strings.Replace(s.URL, "127.0.0.1", "example.com", 1) + "/dns-query"
	endpoint, err := ParseEndpoint(address)
	require.NoError(t, err)
	c, err := New(Options{Endpoints: []Endpoint{endpoint}, BootstrapDNS: []netip.AddrPort{bootstrap}, RootCAs: roots})
	require.NoError(t, err)
	defer c.Close()
	for range 2 {
		r, err := c.Exchange(context.Background(), encryptedQuery(), make([]byte, 65535))
		require.NoError(t, err)
		assert.Equal(t, address, r.Endpoint.String())
		assert.Equal(t, [2]string{strings.TrimPrefix(strings.TrimSuffix(address, "/dns-query"), "https://"), "example.com"}, <-observed)
	}
	assert.EqualValues(t, 2, calls.Load(), "only encrypted endpoint A/AAAA questions go to bootstrap")
}

func TestEncryptedFailureOnlyUsesExplicitFallback(t *testing.T) {
	sentinel, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(testutil.Request) testutil.Response { return testutil.Response{Drop: true} })
	require.NoError(t, err)
	defer sentinel.Close()
	secure, err := ParseEndpoint("tls://" + sentinel.Address())
	require.NoError(t, err)
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return
		}
		b[2] |= 0x80
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(b)
	}))
	defer s.Close()
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	fallback, err := ParseEndpoint(s.URL + "/dns-query")
	require.NoError(t, err)
	c, err := New(Options{Endpoints: []Endpoint{secure}, Fallback: []Endpoint{fallback}, RootCAs: roots, AttemptTimeout: 50 * time.Millisecond})
	require.NoError(t, err)
	defer c.Close()
	r, err := c.Exchange(context.Background(), encryptedQuery(), make([]byte, 65535))
	require.NoError(t, err)
	assert.True(t, r.Fallback)
	assert.Equal(t, 2, r.Attempts)
	assert.Equal(t, "doh", r.Transport)
	// The TCP endpoint speaks ordinary DNS, so a TLS handshake cannot succeed.
	// The same endpoint's UDP listener must never receive the website question.
	for len(sentinel.Requests()) > 0 {
		request := <-sentinel.Requests()
		assert.Equal(t, "tcp", request.Network)
	}
}

func TestDoTFramingReuseAndTrust(t *testing.T) {
	certServer := httptest.NewTLSServer(http.NotFoundHandler())
	defer certServer.Close()
	roots := x509.NewCertPool()
	roots.AddCert(certServer.Certificate())
	listener, err := tls.Listen("tcp", "127.0.0.1:0", certServer.TLS.Clone())
	require.NoError(t, err)
	defer listener.Close()
	var accepted atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			accepted.Add(1)
			go func(conn net.Conn) {
				defer conn.Close()
				for {
					var size [2]byte
					if _, e := io.ReadFull(conn, size[:]); e != nil {
						return
					}
					b := make([]byte, binary.BigEndian.Uint16(size[:]))
					if _, e := io.ReadFull(conn, b); e != nil {
						return
					}
					b[2] |= 0x80
					if _, e := conn.Write(append(size[:], b...)); e != nil {
						return
					}
				}
			}(conn)
		}
	}()
	c := encryptedClient(t, "tls://"+listener.Addr().String(), roots)
	for range 2 {
		r, e := c.Exchange(context.Background(), encryptedQuery(), make([]byte, 65535))
		require.NoError(t, e)
		assert.Equal(t, "dot", r.Transport)
	}
	assert.EqualValues(t, 1, accepted.Load())
	var workers sync.WaitGroup
	errors := make(chan error, 16)
	for range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := c.Exchange(context.Background(), encryptedQuery(), make([]byte, 65535))
			errors <- err
		}()
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		assert.NoError(t, err)
	}
	bad := encryptedClient(t, "tls://"+listener.Addr().String(), x509.NewCertPool())
	_, err = bad.Exchange(context.Background(), encryptedQuery(), make([]byte, 65535))
	assert.Error(t, err)
	c.Close()
	listener.Close()
	<-done
}

func TestEncryptedHandshakeObeysGlobalDeadline(t *testing.T) {
	for _, scheme := range []string{"tls://", "https://"} {
		t.Run(scheme, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				_, _ = io.Copy(io.Discard, conn)
			}()
			address := scheme + listener.Addr().String()
			if scheme == "https://" {
				address += "/dns-query"
			}
			c := encryptedClient(t, address, nil)
			c.options.Timeout = 60 * time.Millisecond
			start := time.Now()
			_, err = c.Exchange(context.Background(), encryptedQuery(), make([]byte, 65535))
			assert.Error(t, err)
			assert.Less(t, time.Since(start), 250*time.Millisecond)
			select {
			case <-finished:
			case <-time.After(250 * time.Millisecond):
				assert.Fail(t, "handshake socket survived global request deadline")
			}
		})
	}
}

func TestEncryptedCloseCancelsActiveWork(t *testing.T) {
	entered := make(chan struct{})
	closed := make(chan struct{})
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
	}))
	s.EnableHTTP2 = true
	s.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			close(closed)
		}
	}
	s.StartTLS()
	defer s.Close()
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	c := encryptedClient(t, s.URL+"/dns-query", roots)
	done := make(chan error, 1)
	go func() { _, err := c.Exchange(context.Background(), encryptedQuery(), make([]byte, 65535)); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		require.FailNow(t, "request not received")
	}
	require.NoError(t, c.Close())
	select {
	case err := <-done:
		assert.Error(t, err)
	case <-time.After(200 * time.Millisecond):
		require.FailNow(t, "close did not cancel request")
	}
	select {
	case <-closed:
	case <-time.After(250 * time.Millisecond):
		assert.Fail(t, "close left an HTTP/2 socket open")
	}
}
