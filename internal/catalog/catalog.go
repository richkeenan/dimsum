// Package catalog describes predefined subscription choices. Availability is a
// dialect audit, not evidence that a feed is currently downloaded or active.
package catalog

import (
	"fmt"
	"time"

	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
)

type Entry struct {
	ID, Label, Description, Homepage, URL, Attribution, Family string
	Dialect                                                    lists.Dialect
	DefaultEnabled, Available                                  bool
	UnavailableReason                                          string
	UpdateInterval                                             time.Duration
}

// Source supplies the declared parser interpretation, including plain entries.
func (e Entry) Source() lists.Source {
	kind := policy.Suffix
	if e.Dialect == lists.Hosts {
		kind = policy.Exact
	}
	return lists.Source{ID: e.ID, Dialect: e.Dialect, DomainKind: kind}
}

// Subscription makes an explicit authoritative selection from catalog metadata.
// Catalog defaults never implicitly enable network access at daemon startup.
func (e Entry) Subscription(enabled bool) (lists.Subscription, error) {
	if enabled && !e.Available {
		return lists.Subscription{}, fmt.Errorf("catalog %s: %s", e.ID, e.UnavailableReason)
	}
	s := e.Source()
	return lists.Subscription{ID: s.ID, URL: e.URL, Dialect: s.Dialect, DomainKind: s.DomainKind, Enabled: enabled}, nil
}

// Entries returns independent metadata values. Family is advisory: owners may
// override overlapping-base-list recommendations. Defaults apply to compatibility
// installation only; callers must explicitly select enabled source versions.
func Entries() []Entry {
	entries := []Entry{
		{ID: "stevenblack-unified", Label: "StevenBlack Unified", Description: "Unified hosts blocklist; compatibility preset", Homepage: "https://github.com/StevenBlack/hosts", URL: "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts", Attribution: "Steven Black and contributing source authors; MIT repository license, individual source licenses retained upstream", Family: "base", Dialect: lists.Hosts, DefaultEnabled: true, Available: true},
		{ID: "hagezi-light", Label: "HaGeZi Light", Description: "Light DNS blocklist", URL: "https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/adblock/light.txt", Family: "base"},
		{ID: "hagezi-normal", Label: "HaGeZi Normal", Description: "Recommended alternative balanced preset", URL: "https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/adblock/multi.txt", Family: "base"},
		{ID: "hagezi-pro", Label: "HaGeZi Pro", Description: "More aggressive DNS blocklist", URL: "https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/adblock/pro.txt", Family: "base"},
		{ID: "hagezi-tif-mini", Label: "HaGeZi Threat Intelligence Mini", Description: "Optional threats feed", URL: "https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/adblock/tif.mini.txt"},
		{ID: "oisd-small", Label: "OISD Small", Description: "Small DNS blocklist", Homepage: "https://oisd.nl/", URL: "https://small.oisd.nl/", Attribution: "OISD / sjhgvr and upstream contributors; see publisher terms at https://oisd.nl/", Family: "base", Dialect: lists.Adblock, Available: true},
		{ID: "oisd-big", Label: "OISD Big", Description: "Expanded DNS blocklist", Homepage: "https://oisd.nl/", URL: "https://big.oisd.nl/", Attribution: "OISD / sjhgvr and upstream contributors; see publisher terms at https://oisd.nl/", Family: "base", Dialect: lists.Adblock, Available: true},
		{ID: "adguard-dns", Label: "AdGuard DNS", Description: "AdGuard DNS filter; richer dialect", Homepage: "https://github.com/AdguardTeam/AdGuardSDNSFilter", URL: "https://adguardteam.github.io/HostlistsRegistry/assets/filter_1.txt", Attribution: "AdGuard Software Ltd and contributors; GPL-3.0, upstream source attribution in feed", Family: "base", Dialect: lists.Adblock, UnavailableReason: "Contains unsupported DNS Adblock modifiers and patterns; candidate must be rejected"},
	}
	for i := range entries {
		e := &entries[i]
		e.UpdateInterval = 24 * time.Hour
		if len(e.ID) >= 7 && e.ID[:7] == "hagezi-" {
			e.Homepage = "https://github.com/hagezi/dns-blocklists"
			e.Attribution = "HaGeZi and upstream contributors; GPL-3.0 repository license, upstream source attribution in repository"
			e.Dialect = lists.Adblock
			e.Available = true
		}
	}
	return entries
}
