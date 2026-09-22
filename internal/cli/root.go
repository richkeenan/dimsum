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
  summary | timeseries | queries | rankings [--query 'from=...&to=...&limit=...']
  settings | lists | rules | records | clients | upstreams | blocking | diagnostics | jobs | catalog | tokens
  query ID
Mutation operations:
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
  job JSON                   {"kind":"refresh|backup|restore|upstream-probe|support-bundle","input":{...}}
  events                     SSE stream; reconnect requires a fresh summary fetch
  request METHOD PATH [JSON] complete HTTP parity, including future operations

Use --query with GET commands for server-side filtering. JSON may be literal,
@FILE, or @- to read standard input (4 MiB maximum).
Socket defaults to DIMSUM_CONTROL_SOCKET or /run/dimsum/control.sock.
Upstream presets (cloudflare, google, quad9):
  add upstreams '{"revision":"...","item":{"preset":"cloudflare"}}'
Adds both provider addresses together, skipping existing servers.
Custom upstreams accept an IP address (port defaults to 53) or IP:port:
  add upstreams '{"revision":"...","item":"192.0.2.53"}'
Test a saved server with job '{"kind":"upstream-probe","input":{"endpoint":"192.0.2.53:53"}}'.
Exit: 0 success, 2 usage, 3 connection/I/O, 4 rejected request, 5 conflict, 6 unavailable.
`

func Run(ctx context.Context, args []string, out, stderr io.Writer) int {
	socket := os.Getenv("DIMSUM_CONTROL_SOCKET")
	if socket == "" {
		socket = "/run/dimsum/control.sock"
	}
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
	case "summary", "timeseries", "queries", "rankings", "settings", "lists", "rules", "records", "clients", "upstreams", "diagnostics", "jobs", "events", "catalog", "tokens":
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
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	if strings.HasPrefix(path, "/api/v1/events") {
		client.Timeout = 0
	}
	req, e := http.NewRequestWithContext(ctx, method, "http://local"+path, strings.NewReader(body))
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 2
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := client.Do(req)
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 3
	}
	defer resp.Body.Close()
	dst := out
	if resp.StatusCode >= 400 {
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
	if resp.StatusCode >= 400 {
		return 4
	}
	return 0
}
