package lists

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type Fetcher struct {
	Client                     *http.Client
	MaxCompressed, MaxExpanded int64
	Timeout                    time.Duration
}

func NewFetcher(client *http.Client) *Fetcher {
	if client == nil {
		client = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DisableCompression: true, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second}}
	}
	return &Fetcher{Client: client, MaxCompressed: 32 << 20, MaxExpanded: 128 << 20, Timeout: 30 * time.Second}
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
