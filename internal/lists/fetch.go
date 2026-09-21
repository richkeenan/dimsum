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
)

type Fetcher struct {
	Client                     *http.Client
	MaxCompressed, MaxExpanded int64
	Timeout                    time.Duration
}

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
