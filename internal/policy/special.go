package policy

import (
	"encoding/binary"
	"fmt"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"net/netip"
	"time"
)

type Settings struct {
	Mode               string    `yaml:"mode,omitempty"`
	TTL                uint32    `yaml:"ttl,omitempty"`
	PauseUntil         time.Time `yaml:"pause_until,omitempty"`
	MozillaCanary      bool      `yaml:"mozilla_canary,omitempty"`
	PrivateRelay       bool      `yaml:"private_relay,omitempty"`
	DesignatedResolver bool      `yaml:"designated_resolver,omitempty"`
	SinkholeIPv4       string    `yaml:"sinkhole_ipv4,omitempty"`
	SinkholeIPv6       string    `yaml:"sinkhole_ipv6,omitempty"`
}

func (s Settings) Paused(now time.Time) bool { return now.Before(s.PauseUntil) }
func (s Settings) BlockTTL() uint32 {
	if s.TTL == 0 {
		return 2
	}
	return s.TTL
}
func (s Settings) Validate() error {
	switch s.Mode {
	case "", "null", "nxdomain", "nodata", "refused", "sinkhole":
	default:
		return fmt.Errorf("unsupported block mode")
	}
	for _, v := range []struct {
		s  string
		v4 bool
	}{{s.SinkholeIPv4, true}, {s.SinkholeIPv6, false}} {
		if v.s == "" {
			continue
		}
		a, e := netip.ParseAddr(v.s)
		if e != nil || a.Zone() != "" || a.Is4() != v.v4 {
			return fmt.Errorf("invalid sinkhole address")
		}
	}
	if s.Mode == "sinkhole" && s.SinkholeIPv4 == "" && s.SinkholeIPv6 == "" {
		return fmt.Errorf("sinkhole requires an address")
	}
	return nil
}
func (s Settings) Rules() []Rule {
	var rules []Rule
	add := func(name string) {
		rules = append(rules, Rule{ID: "special:" + name, SourceID: "special", Pattern: name, Kind: Exact, Class: SpecialDeny})
	}
	if s.MozillaCanary {
		add("use-application-dns.net")
	}
	if s.PrivateRelay {
		add("mask.icloud.com")
		add("mask-h2.icloud.com")
	}
	if s.DesignatedResolver {
		add("_dns.resolver.arpa")
	}
	return rules
}
func BuildBlocked(dst []byte, q *dnswire.Message, s Settings) (int, error) {
	var code uint16
	var answers, authority []dnswire.SyntheticRecord
	switch s.Mode {
	case "nxdomain":
		code = 3
	case "refused":
		code = 5
	}
	if s.Mode == "" || s.Mode == "null" || s.Mode == "sinkhole" {
		var data []byte
		switch q.Question.Type {
		case 1:
			if s.Mode != "sinkhole" {
				data = make([]byte, 4)
			} else if s.SinkholeIPv4 != "" {
				a, e := netip.ParseAddr(s.SinkholeIPv4)
				if e != nil {
					return 0, e
				}
				data = a.AsSlice()
			}
		case 28:
			if s.Mode != "sinkhole" {
				data = make([]byte, 16)
			} else if s.SinkholeIPv6 != "" {
				a, e := netip.ParseAddr(s.SinkholeIPv6)
				if e != nil {
					return 0, e
				}
				data = a.AsSlice()
			}
		}
		if data != nil {
			answers = append(answers, dnswire.SyntheticRecord{Name: q.Question.Name.Wire[:q.Question.Name.Length], Type: q.Question.Type, TTL: s.BlockTTL(), Data: data})
		}
	}
	if len(answers) == 0 && code != 5 {
		authority = append(authority, dnswire.NegativeSOA([]byte{0}, s.BlockTTL()))
	}
	n, err := dnswire.BuildSynthetic(dst, q, code, false, answers, authority)
	if err == nil && q.EDNS.Present && n+6 <= len(dst) && n+6 <= dnswire.UDPBudget(q.EDNS, 1232) {
		// The synthetic OPT is last with empty RDATA. EDE Blocked has no private text.
		binary.BigEndian.PutUint16(dst[n-2:n], 6)
		copy(dst[n:n+6], []byte{0, 15, 0, 2, 0, 15})
		n += 6
	}
	return n, err
}
