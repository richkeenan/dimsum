// Package cli is a lightweight JSON client for the running coordinator.
package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const Help = `dimsum control [--socket PATH] COMMAND

Read operations (JSON):
  summary | timeseries | performance | queries | rankings [--query 'from=...&to=...']
  settings | lists | rules | records | clients | upstreams | blocking | diagnostics | jobs | catalog | tokens
  query ID
  dhcp | dhcp-status | dhcp-leases | dhcp-reservations
  get dhcp|dhcp-status|dhcp-leases|dhcp-reservations
Mutation operations:
  patch dhcp JSON            revision and relative scalar edits
  add dhcp/reservations JSON revision and reservation item
  patch dhcp/reservations/ID JSON  revision and relative scalar edits
  delete dhcp/reservations/ID JSON revision only
  password JSON             {"password":"..."} (use @- for standard input)
  token-create JSON         {"name":"automation"} (secret returned only once)
  token-revoke ID            revoke an API token immediately
  patch RESOURCE JSON       {"revision":"...","edits":[{"path":["..."],"value":...}]}
  add RESOURCE JSON         {"revision":"...","item":{...}}
  delete RESOURCE JSON      {"revision":"...","index":0}
  blocking JSON             {"revision":"...","enabled":false,"pause_until":"RFC3339"}
  rules-test JSON            {"name":"example.org","generation":"1"}
  stage JSON                 revision and grouped scalar edits
  commit ID
  job JSON                   {"kind":"refresh|backup|restore|upstream-probe|support-bundle|dhcp-check","input":{...}}
  events                     SSE stream; reconnect requires a fresh summary fetch
  request METHOD PATH [JSON] complete HTTP parity, including future operations

Use --query with GET commands for server-side filtering. JSON may be literal,
@FILE, or @- to read standard input (4 MiB maximum).
Performance accepts resolution_seconds=60|3600|86400 (at most 1500 buckets).
It reports server-side average/p50/p95/p99, outcome timings and distribution.
Percentiles are histogram estimates (up to 3.125% error), null for legacy data
without fine timing coverage. Admission rejections are excluded.
Run dimsum login first to save an authenticated HTTP connection for your user.
Run dimsum logout to revoke the saved token and remove the connection.
For local recovery, --socket PATH or DIMSUM_CONTROL_SOCKET overrides HTTP login.
Upstream presets (cloudflare, google, quad9):
  add upstreams '{"revision":"...","item":{"preset":"cloudflare","transport":"https"}}'
Adds provider endpoints atomically, skipping existing servers.
Provider transport is https (encrypted DoH) or plain (standard IPv4 pair).
Omitting transport retains legacy plain behavior. Google and Quad9 are also available.
Custom upstreams accept an IP address (port defaults to 53), IP:port,
https://host/path (DoH), or tls://host[:port] (DoT, default port 853):
  add upstreams '{"revision":"...","item":"192.0.2.53"}'
  add upstreams '{"revision":"...","item":"tls://dns.example"}'
Test a saved server with job '{"kind":"upstream-probe","input":{"endpoint":"192.0.2.53:53"}}'.
Encrypted failures never implicitly downgrade to plaintext. Bootstrap DNS resolves
only encrypted server hostnames; omitted bootstrap settings use 1.1.1.1:53 and 9.9.9.9:53.
Set or reset advanced bootstrap DNS through shared settings edits:
  patch settings '{"revision":"...","edits":[{"path":["dns","bootstrap_dns"],"value":["1.1.1.1:53","9.9.9.9:53"]}]}'
Exit: 0 success, 2 usage, 3 connection/I/O, 4 rejected request, 5 conflict, 6 unavailable.
`

func Run(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "login" {
		return login(ctx, args[1:], out, stderr)
	}
	if len(args) > 0 && args[0] == "logout" {
		return logout(ctx, args[1:], out, stderr)
	}
	socket := os.Getenv("DIMSUM_CONTROL_SOCKET")
	if len(args) > 0 && args[0] == "control" {
		args = args[1:]
	}
	if len(args) >= 2 && args[0] == "--socket" {
		socket = args[1]
		args = args[2:]
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprint(out, Help)
		return 0
	}
	method, path, body := "GET", "/api/v1/"+args[0], ""
	query := ""
	if len(args) >= 3 && args[len(args)-2] == "--query" {
		query = args[len(args)-1]
		args = args[:len(args)-2]
	}
	bad := func() int { fmt.Fprintln(stderr, "invalid arguments; run dimsum control help"); return 2 }
	switch args[0] {
	case "get":
		if len(args) != 2 {
			return bad()
		}
		p, ok := dhcpPaths[args[1]]
		if !ok {
			return bad()
		}
		path = p
	case "dhcp", "dhcp-status", "dhcp-leases", "dhcp-reservations":
		if len(args) != 1 {
			return bad()
		}
		path = dhcpPaths[args[0]]
	case "patch", "add", "delete":
		if len(args) != 3 {
			return bad()
		}
		method = map[string]string{"patch": "PATCH", "add": "POST", "delete": "DELETE"}[args[0]]
		path = "/api/v1/" + args[1]
		body = args[2]
	case "blocking":
		if len(args) == 2 {
			method = "PUT"
			body = args[1]
		} else if len(args) != 1 {
			return bad()
		}
	case "rules-test", "stage", "job", "password", "token-create":
		if len(args) != 2 {
			return bad()
		}
		method = "POST"
		path = map[string]string{"rules-test": "/api/v1/rules/test", "stage": "/api/v1/config/transactions", "job": "/api/v1/jobs", "password": "/api/v1/password", "token-create": "/api/v1/tokens"}[args[0]]
		body = args[1]
	case "token-revoke":
		if len(args) != 2 || args[1] == "" {
			return bad()
		}
		method = "DELETE"
		path = "/api/v1/tokens/" + url.PathEscape(args[1])
	case "commit":
		if len(args) != 2 {
			return bad()
		}
		method = "POST"
		path = "/api/v1/config/transactions/" + url.PathEscape(args[1]) + "/commit"
	case "query":
		if len(args) != 2 {
			return bad()
		}
		path = "/api/v1/queries/" + url.PathEscape(args[1])
	case "request":
		if len(args) < 3 || len(args) > 4 {
			return bad()
		}
		method, path = args[1], args[2]
		if len(args) == 4 {
			body = args[3]
		}
	case "summary", "timeseries", "performance", "queries", "rankings", "settings", "lists", "rules", "records", "clients", "upstreams", "diagnostics", "jobs", "events", "catalog", "tokens":
		if len(args) != 1 {
			return bad()
		}
	default:
		return bad()
	}
	if !strings.HasPrefix(path, "/api/v1/") && !strings.HasPrefix(path, "/health/") {
		return bad()
	}
	if query != "" {
		if strings.Contains(path, "?") {
			return bad()
		}
		path += "?" + query
	}
	if strings.HasPrefix(body, "@") {
		var reader io.Reader = os.Stdin
		if body != "@-" {
			f, err := os.Open(strings.TrimPrefix(body, "@"))
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 3
			}
			defer f.Close()
			reader = f
		}
		data, err := io.ReadAll(io.LimitReader(reader, (4<<20)+1))
		if err != nil || len(data) > 4<<20 {
			fmt.Fprintln(stderr, "cannot read JSON body within 4 MiB limit")
			return 3
		}
		body = string(data)
	}
	client := httpClient()
	base, token := "http://local", ""
	if socket != "" {
		transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
		}}
		defer transport.CloseIdleConnections()
		client.Transport = transport
	} else {
		c, err := readCredentials()
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintln(stderr, "Not logged in. Run dimsum login to connect to the local server, or dimsum login --server URL for another dashboard address.")
			} else {
				fmt.Fprintln(stderr, "Cannot read saved CLI login:", err)
			}
			return 3
		}
		base, token = c.Server, c.Token
	}
	if strings.HasPrefix(path, "/api/v1/events") {
		client.Timeout = 0
	}
	req, e := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(body))
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 2
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, e := client.Do(req)
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 3
	}
	defer resp.Body.Close()
	dst := out
	if resp.StatusCode >= 300 {
		dst = stderr
	}
	if _, e = io.Copy(dst, resp.Body); e != nil {
		fmt.Fprintln(stderr, e)
		return 3
	}
	switch resp.StatusCode {
	case 409:
		return 5
	case 503:
		return 6
	}
	if resp.StatusCode == 401 {
		fmt.Fprintln(stderr, "CLI login is no longer valid; run dimsum logout, then dimsum login")
	}
	if resp.StatusCode >= 300 {
		return 4
	}
	return 0
}

var dhcpPaths = map[string]string{"dhcp": "/api/v1/dhcp", "dhcp-status": "/api/v1/dhcp/status", "dhcp-leases": "/api/v1/dhcp/leases", "dhcp-reservations": "/api/v1/dhcp/reservations"}
