# Encrypted upstream DNS

dimsum supports DNS-over-HTTPS (DoH) and DNS-over-TLS (DoT). Existing IP:port
upstreams continue to use UDP, with TCP retry for truncated replies.

```yaml
dns:
  upstreams:
    - https://cloudflare-dns.com/dns-query
    - tls://dns.google
  bootstrap_dns:
    - 1.1.1.1:53
    - 9.9.9.9:53
```

HTTPS endpoints require a path; their default port is 443. TLS endpoints default
to port 853 and cannot contain a path or query. Both permit explicit ports and
reject credentials and fragments. Certificates are verified against system trust
and the configured hostname. There is no certificate-verification bypass option.

## Add and edit servers

Read `dimsum control upstreams` for the current saved revision, then add a provider:

```sh
dimsum control add upstreams '{"revision":"CURRENT_REVISION","item":{"preset":"cloudflare","transport":"https"}}'
```

The same body works with `POST /api/v1/upstreams` and MCP `add_upstream`.
Presets expand as follows:

| Preset | `https` | `plain` |
| --- | --- | --- |
| `cloudflare` | `https://cloudflare-dns.com/dns-query` | `1.1.1.1:53`, `1.0.0.1:53` |
| `google` | `https://dns.google/dns-query` | `8.8.8.8:53`, `8.8.4.4:53` |
| `quad9` | `https://dns.quad9.net/dns-query` | `9.9.9.9:53`, `149.112.112.112:53` |

Omitting `transport` retains the API's legacy `plain` behavior. Additions are
atomic and skip existing identical endpoints. Custom `item` strings accept
IP addresses (port 53 by default), IP:port, DoH URLs and DoT URLs. Existing
revision-checked PATCH and DELETE operations also apply to encrypted entries.

Read the saved configuration and activation status after a mutation. Saving a
URL does not prove that the server can answer DNS queries.

## Bootstrap DNS

Bootstrap resolvers locate encrypted server hostnames without using the system
resolver. They resolve only those server hostnames; website DNS questions go
through the configured upstream pool. Bootstrap DNS itself is standard DNS.
Answers are cached within DNS TTL and bounded resource limits.

When `dns.bootstrap_dns` is omitted, defaults are `1.1.1.1:53` and `9.9.9.9:53`.
To customize the list, read `dimsum control settings`, then use a settings edit:

```sh
dimsum control patch settings '{"revision":"CURRENT_REVISION","edits":[{"path":["dns","bootstrap_dns"],"value":["192.0.2.53:53"]}]}'
```

This inserts the list when absent and replaces it when present. The same body
works with `PATCH /api/v1/settings` and MCP `update_settings`. To reset the
effective behavior, save `["1.1.1.1:53","9.9.9.9:53"]`. This writes explicit
defaults; it does not remove the YAML key. Empty lists, encrypted bootstrap URLs,
and listener loops are invalid. Lists allow 1–16 literal unicast IP:port entries.
The editor preserves unrelated text and refuses ambiguous commented entry
layouts rather than discarding comments.

## Test a saved connection

```sh
dimsum control job '{"kind":"upstream-probe","input":{"endpoint":"https://cloudflare-dns.com/dns-query","timeout_ms":1000}}'
dimsum control jobs
```

The endpoint must belong to the active primary or fallback pool. The diagnostic
uses a private client, configured bootstrap DNS, certificate verification and a
validated root NS query. `healthy` means a valid NOERROR response; `responding`
means a validated DNS response, including DNS error responses. A successful TLS
handshake alone is not success. Results report `udp`, `tcp`, `tls` or `https`,
elapsed time and failure details. On failure, transport identifies the configured
protocol rather than claiming connectivity. Configuration reload retires the
private client's transport resources.

Encrypted failures never synthesize a plaintext fallback. Only explicitly
configured primary and fallback endpoints can be tried. A mixed pool can send
website queries over plaintext when one of its explicitly configured standard
servers is selected. Encryption protects traffic between dimsum and the upstream
resolver; it does not change the client-to-dimsum connection or the resolver's
filtering policy.
