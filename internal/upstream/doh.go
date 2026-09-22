package upstream

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/richkeenan/dimsum/internal/dnswire"
)

type dohAttemptKey struct{}

func (c *Client) dohClient(e Endpoint) *http.Client {
	c.httpMu.Lock()
	defer c.httpMu.Unlock()
	if h := c.httpClients[e]; h != nil {
		return h
	}
	tr := &http.Transport{
		Proxy: nil, ForceAttemptHTTP2: true, DisableCompression: true,
		MaxConnsPerHost: c.options.MaxOutstanding, MaxIdleConnsPerHost: 2, MaxIdleConns: 2,
		IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: c.options.AttemptTimeout,
		MaxResponseHeaderBytes: 16 << 10,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, ServerName: e.host, RootCAs: c.options.RootCAs, NextProtos: []string{"h2", "http/1.1"}},
	}
	tr.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		// net/http detaches request cancellation from shared dials. Preserve
		// the originating attempt's deadline through bootstrap, TCP and TLS.
		if attempt, ok := ctx.Value(dohAttemptKey{}).(context.Context); ok {
			ctx = attempt
		}
		ctx, cancel := context.WithTimeout(ctx, c.options.AttemptTimeout)
		defer cancel()
		stop := context.AfterFunc(c.lifetime, cancel)
		defer stop()
		return c.dialEndpoint(ctx, e)
	}
	h := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	c.httpClients[e] = h
	return h
}
func (c *Client) exchangeDoH(ctx context.Context, e Endpoint, query []byte, q *dnswire.Question, id uint16, out []byte) (int, dnswire.Message, error) {
	ctx = context.WithValue(ctx, dohAttemptKey{}, ctx)
	// RoundTrip can finish before its request-body writer. Retrying must not
	// mutate the ID underneath that writer.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.String(), bytes.NewReader(bytes.Clone(query)))
	if err != nil {
		return 0, dnswire.Message{}, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	resp, err := c.dohClient(e).Do(req)
	if err != nil {
		return 0, dnswire.Message{}, err
	}
	defer resp.Body.Close()
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode != http.StatusOK || err != nil || media != "application/dns-message" || resp.ContentLength > 65535 || resp.Header.Get("Content-Encoding") != "" {
		return 0, dnswire.Message{}, fmt.Errorf("upstream: invalid DoH response (HTTP %d)", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if err != nil {
		return 0, dnswire.Message{}, err
	}
	if len(body) > 65535 || len(body) > len(out) {
		return 0, dnswire.Message{}, dnswire.ErrBounds
	}
	m, err := validate(body, q, id)
	if err != nil {
		return 0, m, err
	}
	copy(out, body)
	return len(body), m, nil
}
