package lists

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"sync/atomic"
	"time"

	"github.com/richkeenan/dimsum/internal/upstream"
)

type Fetcher struct {
	Client                     *http.Client
	MaxCompressed, MaxExpanded int64
	Timeout                    time.Duration
}

type fetchContextKey struct{}

func NewFetcher(client *http.Client) *Fetcher {
	if client == nil {
		client = bootstrapClient(nil)
	}
	return &Fetcher{Client: client, MaxCompressed: 32 << 20, MaxExpanded: 128 << 20, Timeout: 30 * time.Second}
}

// NewFetcherWithUpstreams resolves subscription (and HTTP proxy) hostnames via
// literal configured DNS endpoints, never the host's DNS service. The caller
// rejects endpoints that point back to its own listeners. An empty set fails
// hostname lookups closed, while literal HTTP destinations still work.
func NewFetcherWithUpstreams(upstreams []string) (*Fetcher, error) {
	endpoints := make([]netip.AddrPort, len(upstreams))
	if len(upstreams) > 16 {
		return nil, fmt.Errorf("lists: at most 16 bootstrap endpoints")
	}
	for i, address := range upstreams {
		endpoint, err := netip.ParseAddrPort(address)
		if err != nil || endpoint.Port() == 0 || endpoint.Addr().Unmap().IsUnspecified() || endpoint.Addr().Unmap().IsMulticast() {
			return nil, fmt.Errorf("lists: bootstrap endpoint must be a unicast literal IP and nonzero port")
		}
		endpoints[i] = endpoint
	}
	return NewFetcher(bootstrapClient(endpoints)), nil
}

// NewFetcherWithOptions applies the same DNS transport policy as forwarding.
// A lookup-scoped client owns and closes encrypted sockets after each dial;
// the HTTP transport still reuses subscription connections normally.
func NewFetcherWithOptions(options upstream.Options) (*Fetcher, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return newFetcherWithDial(options, dialer.DialContext)
}

func newFetcherWithDial(options upstream.Options, dial func(context.Context, string, string) (net.Conn, error)) (*Fetcher, error) {
	if len(options.Endpoints) == 0 {
		return NewFetcher(nil), nil
	}
	if err := upstream.ValidateOptions(options); err != nil {
		return nil, err
	}
	options.Endpoints = append([]upstream.Endpoint(nil), options.Endpoints...)
	options.Fallback = append([]upstream.Endpoint(nil), options.Fallback...)
	if options.BootstrapDNS != nil {
		options.BootstrapDNS = append([]netip.AddrPort{}, options.BootstrapDNS...)
	}
	if options.RootCAs != nil {
		options.RootCAs = options.RootCAs.Clone()
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, DisableCompression: true, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		// HTTP detaches dial cancellation from its request. Keep both the
		// originating fetch lifetime and transport cancellation authoritative.
		origin, _ := ctx.Value(fetchContextKey{}).(context.Context)
		budget := 10 * time.Second
		if origin != nil {
			if deadline, ok := origin.Deadline(); ok {
				budget = min(budget, time.Until(deadline))
			}
		}
		ctx, cancel := context.WithTimeout(ctx, budget)
		defer cancel()
		if origin != nil {
			stop := context.AfterFunc(origin, cancel)
			defer stop()
			if origin.Err() != nil {
				return nil, origin.Err()
			}
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if _, err := netip.ParseAddr(host); err == nil {
			return dial(ctx, network, address)
		}
		client, err := upstream.New(options)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		addresses, err := client.LookupIP(ctx, host)
		if err != nil {
			return nil, err
		}
		addresses = alternateFamilies(addresses)
		for i, a := range addresses {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// Reserve a fair share for every remaining candidate, including
			// candidates within one family. No literal gets a new ten seconds.
			deadline, _ := ctx.Deadline()
			candidate, stop := context.WithTimeout(ctx, time.Until(deadline)/time.Duration(len(addresses)-i))
			conn, e := dial(candidate, network, net.JoinHostPort(a.String(), port))
			stop()
			if e == nil {
				if err := ctx.Err(); err != nil {
					conn.Close()
					return nil, err
				}
				return conn, nil
			}
			err = e
		}
		return nil, err
	}
	return NewFetcher(&http.Client{Transport: transport}), nil
}

// Preserve the preferred family and within-family order, while giving the
// other family its first opportunity immediately after the first candidate.
func alternateFamilies(addresses []netip.Addr) []netip.Addr {
	if len(addresses) < 2 {
		return addresses
	}
	var preferred, alternate []netip.Addr
	for _, a := range addresses {
		if a.Is4() == addresses[0].Is4() {
			preferred = append(preferred, a)
		} else {
			alternate = append(alternate, a)
		}
	}
	out := make([]netip.Addr, 0, len(addresses))
	for i := 0; i < max(len(preferred), len(alternate)); i++ {
		if i < len(preferred) {
			out = append(out, preferred[i])
		}
		if i < len(alternate) {
			out = append(out, alternate[i])
		}
	}
	return out
}

func bootstrapClient(endpoints []netip.AddrPort) *http.Client {
	var next atomic.Uint64
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		if len(endpoints) == 0 {
			return nil, fmt.Errorf("lists: no explicit bootstrap DNS upstreams configured")
		}
		// Resolver owns DNS message framing, UDP retries and TCP fallback. Its
		// system-server address is deliberately ignored; only these literals dial.
		endpoint := endpoints[(next.Add(1)-1)%uint64(len(endpoints))]
		dialer := net.Dialer{Timeout: 5 * time.Second}
		return dialer.DialContext(ctx, network, endpoint.String())
	}}
	dialer := &net.Dialer{Resolver: resolver, Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment, DialContext: dialer.DialContext,
		DisableCompression: true, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}}
}

// Explicit Accept-Encoding prevents net/http's transparent decompression from
// bypassing the compressed-byte budget, including when using an injected client.
func (f *Fetcher) fetch(ctx context.Context, s Subscription, old *sourceArtifact) (body []byte, etag, modified string, unchanged bool, err error) {
	if f.MaxCompressed <= 0 || f.MaxCompressed > 32<<20 || f.MaxExpanded <= 0 || f.MaxExpanded > 128<<20 || f.Timeout <= 0 {
		return nil, "", "", false, fmt.Errorf("lists: invalid fetch budget")
	}
	ctx, cancel := context.WithTimeout(ctx, f.Timeout)
	defer cancel()
	ctx = context.WithValue(ctx, fetchContextKey{}, ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return nil, "", "", false, err
	}
	req.Header.Set("Accept-Encoding", "gzip")
	if old != nil {
		req.Header.Set("If-None-Match", old.ETag)
		req.Header.Set("If-Modified-Since", old.Modified)
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, "", "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 304 {
		if old == nil {
			return nil, "", "", false, fmt.Errorf("lists: 304 without cached version")
		}
		return nil, "", "", true, nil
	}
	if resp.StatusCode != 200 {
		return nil, "", "", false, fmt.Errorf("lists: HTTP %d", resp.StatusCode)
	}
	if resp.Uncompressed || resp.ContentLength > f.MaxCompressed {
		return nil, "", "", false, fmt.Errorf("lists: compressed transfer limit/transparent decoding")
	}
	bounded := &io.LimitedReader{R: resp.Body, N: f.MaxCompressed + 1}
	var r io.Reader = bounded
	switch resp.Header.Get("Content-Encoding") {
	case "", "identity":
	case "gzip":
		z, e := gzip.NewReader(bounded)
		if e != nil {
			return nil, "", "", false, e
		}
		defer z.Close()
		r = z
	default:
		return nil, "", "", false, fmt.Errorf("lists: unsupported content encoding")
	}
	temp, err := os.CreateTemp("", "dimsum-download-*")
	if err != nil {
		return nil, "", "", false, err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	n, err := io.Copy(temp, io.LimitReader(r, f.MaxExpanded+1))
	if err != nil {
		return nil, "", "", false, err
	}
	if n > f.MaxExpanded || bounded.N == 0 {
		return nil, "", "", false, fmt.Errorf("lists: download size limit exceeded")
	}
	if _, err = temp.Seek(0, 0); err != nil {
		return nil, "", "", false, err
	}
	body, err = io.ReadAll(temp)
	return body, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), false, err
}
