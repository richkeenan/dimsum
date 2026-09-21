package clients

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouterPTRClosesTruncatedLookupTCP(t *testing.T) {
	for _, negative := range []bool{false, true} {
		t.Run(fmt.Sprint("negative=", negative), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			udp, err := net.ListenPacket("udp", listener.Addr().String())
			require.NoError(t, err)
			defer udp.Close()
			done := make(chan error, 1)
			go func() {
				done <- func() error {
					_ = udp.SetDeadline(time.Now().Add(time.Second))
					buf := make([]byte, 65535)
					n, peer, e := udp.ReadFrom(buf)
					if e != nil {
						return e
					}
					buf[2] |= 0x82 // QR and TC force private PTR retry over TCP.
					if _, e = udp.WriteTo(buf[:n], peer); e != nil {
						return e
					}
					_ = listener.(*net.TCPListener).SetDeadline(time.Now().Add(time.Second))
					conn, e := listener.Accept()
					if e != nil {
						return e
					}
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(time.Second))
					var size [2]byte
					if _, e = io.ReadFull(conn, size[:]); e != nil {
						return e
					}
					n = int(binary.BigEndian.Uint16(size[:]))
					if _, e = io.ReadFull(conn, buf[:n]); e != nil {
						return e
					}
					var q dns.Msg
					if e = q.Unpack(buf[:n]); e != nil {
						return e
					}
					if len(q.Question) != 1 || q.Question[0].Name != "2.1.168.192.in-addr.arpa." || q.Question[0].Qtype != dns.TypePTR {
						return fmt.Errorf("unexpected private PTR: %v", q.Question)
					}
					r := new(dns.Msg).SetReply(&q)
					if negative {
						r.Rcode = dns.RcodeNameError
					} else {
						r.Answer = []dns.RR{&dns.PTR{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: 60}, Ptr: "device.home.arpa."}}
					}
					wire, e := r.Pack()
					if e != nil {
						return e
					}
					binary.BigEndian.PutUint16(size[:], uint16(len(wire)))
					if _, e = conn.Write(append(size[:], wire...)); e != nil {
						return e
					}
					_, e = conn.Read(buf[:1])
					if e != io.EOF {
						return fmt.Errorf("lookup TCP connection not closed: %v", e)
					}
					return nil
				}()
			}()
			name, ttl, err := routerPTR(context.Background(), listener.Addr().String(), netip.MustParseAddr("192.168.1.2"))
			require.NoError(t, err)
			if negative {
				assert.Empty(t, name)
				assert.Zero(t, ttl)
			} else {
				assert.Equal(t, "device.home.arpa", name)
				assert.Equal(t, uint32(60), ttl)
			}
			require.NoError(t, <-done)
		})
	}
}

func TestNegativeDiscoveryRetainsAttemptedSource(t *testing.T) {
	file := filepath.Join(t.TempDir(), "hosts")
	require.NoError(t, os.WriteFile(file, []byte("192.168.1.3 other.home.arpa\n"), 0600))
	for _, tc := range []struct {
		name      string
		settings  Settings
		source    string
		wantError bool
	}{
		{"no-source", Settings{}, "unknown", false},
		{"hosts-miss", Settings{HostsFile: file}, "hosts", false},
		{"hosts-error", Settings{HostsFile: file + ".missing"}, "hosts", true},
		{"ptr-error", Settings{Resolver: "127.0.0.1:53"}, "router-ptr", true},
		{"hosts-then-ptr-error", Settings{HostsFile: file, Resolver: "127.0.0.1:53"}, "router-ptr", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view, err := NewView(tc.settings, nil, nil)
			require.NoError(t, err)
			// Cancellation exercises the PTR error path without sending traffic.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			got := discover(ctx, view, netip.MustParseAddr("192.168.1.2"))
			assert.True(t, got.Negative)
			assert.Empty(t, got.Name)
			assert.Equal(t, tc.source, got.Source)
			assert.Equal(t, tc.wantError, got.Error != "")
		})
	}
}
