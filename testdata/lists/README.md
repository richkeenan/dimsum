# Subscription parser fixtures and audit

These are small excerpts of feeds downloaded on **2026-09-21**. Rule lines are
verbatim; attribution comments were shortened/added for these fixtures. Synthetic
edge cases live in `internal/lists/parse_test.go`. These excerpts are syntax
regressions, not complete protective lists. URLs are in `internal/catalog/catalog.go`.

## Attribution

- `stevenblack-unified.txt`: Steven Black and contributing authors,
  [StevenBlack/hosts](https://github.com/StevenBlack/hosts), repository
  [MIT license](https://github.com/StevenBlack/hosts/blob/master/license.txt),
  copyright © 2023 Steven Black. Individual aggregated sources retain their licenses.
- `hagezi-*.txt`: HaGeZi and contributing authors,
  [hagezi/dns-blocklists](https://github.com/hagezi/dns-blocklists),
  [GPL-3.0](https://github.com/hagezi/dns-blocklists/blob/main/LICENSE).
- `oisd-*.txt`: OISD / sjhgvr and contributing authors,
  [publisher](https://oisd.nl/). The downloaded headers did not state a license;
  attribution is not a claim of a blanket redistribution license for the full feed.
- `adguard-dns.txt`: AdGuard Software Ltd and contributing authors,
  [AdGuardSDNSFilter](https://github.com/AdguardTeam/AdGuardSDNSFilter),
  [GPL-3.0](https://github.com/AdguardTeam/AdGuardSDNSFilter/blob/master/LICENSE).

## Full input audit (original strict policy)

The counts below record the original strict audit. The current parser skips
malformed domain blocking entries with diagnostics rather than rejecting the
whole feed. HaGeZi TIF Mini and OISD Big are now available: their one and three
invalid-domain lines respectively can be omitted. Their checked-in excerpts pass
with these warnings. AdGuard DNS remains unavailable because unsupported syntax
and exceptions still reject the entire candidate. No fresh full-feed download
is implied by this policy update.

All eight catalog URLs returned HTTP 200. Downloads used a 120-second timeout
and 128 MiB expanded-body limit, into local temporary files. The final parser
was run against those same bytes after adding conventional header/boilerplate
support. Counts below precede whole-source rejection: accepted counts for an
unavailable source **are not usable rules**. Nothing was published or installed.

| Source | Bytes | Lines | Parsed rules | Rejected lines | Available |
|---|---:|---:|---:|---:|---|
| stevenblack-unified | 2311184 | 83050 | 76229 | 0 | Yes |
| hagezi-light | 892377 | 39475 | 39461 | 0 | Yes |
| hagezi-normal | 4601138 | 200541 | 200527 | 0 | Yes |
| hagezi-pro | 5050452 | 228014 | 228000 | 0 | Yes |
| hagezi-tif-mini | 3875853 | 183399 | 183385 | 1 | No |
| oisd-small | 1276759 | 56820 | 56808 | 0 | Yes |
| oisd-big | 5688788 | 246372 | 246357 | 3 | No |
| adguard-dns | 4344280 | 181565 | 180604 | 952 | No |

StevenBlack contains `philadelphia_cbslocal.us.intellitxt.com`, a valid ASCII
underscore DNS label now accepted by the shared policy normalizer. Re-auditing
the same frozen bytes after this compatibility correction accepts every line.
TIF Mini contains an invalid IDNA A-label,
`xn--ildcard-0c2c.facture-rapide.fr`. OISD Big contains three invalid IDNA names
(included in its fixture). AdGuard rejects 29 invalid names, 72 IP literals,
196 unsupported exceptions, 17 modifier lines, and 638 other unsupported syntax
lines (wildcards, unanchored/partial patterns, regex and resource syntax).
Classification gives modifiers priority over other syntax on the same line.

StevenBlack is the **enabled compatibility default**. All other entries ship off;
HaGeZi Light/Normal/Pro, TIF Mini, OISD Small and OISD Big can be selected explicitly.
Availability records this audit only: every
future version must pass the parser again before publication. Download failure,
truncation detection beyond syntax/empty/size checks, deletion review, refresh,
last-known-good retention, and activation belong to the update transaction.

SHA-256 of the complete downloaded bytes:

```text
stevenblack-unified a639701fa54b53bdaf5b7e344869bfba497c200d05a3e0f357d35b7bd461c208
hagezi-light        6e176761d83392d99243dc4d6d312f5f0690442f9c5eff1d2c16b15e25519c56
hagezi-normal       3b8e3dcb2dced6a44b83aff1b0cab5351a6253452f0cc1fccfdada94f64257a6
hagezi-pro          2409c843d4a2b8b9dc2f012ef4d50ce18fe3dddd34a7550772e81fd7de11121f
hagezi-tif-mini     61a71f9507b74d6df7f47e77842c6f9dffd86e33baf8d5eb0fb1c426565a871a
oisd-small          e6629d6555b4b2f9c855d1f71e756d3d965991dd3ed70d74f20528ab14fcabd3
oisd-big            1693a7be69a141bca6b44a1fd749eada2a16bf3a9533bc51c625a0ce742f3f54
adguard-dns         78839df7db2d16b8fb0eda160467aa24694c38acb587baa17ae72bb5abd51238
```
