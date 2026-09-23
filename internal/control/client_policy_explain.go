package control

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/policy"
)

type ClientPolicyExplain struct {
	Name       string `json:"name"`
	QType      string `json:"qtype,omitempty"`
	Generation string `json:"generation,omitempty"`
	ClientID   string `json:"client_id,omitempty"`
	Address    string `json:"address,omitempty"`
}
type PolicyDecision struct {
	Result     policy.Result `json:"result"`
	Generation string        `json:"generation"`
	RuleID     string        `json:"rule_id"`
	SourceIDs  []string      `json:"source_ids"`
	Scope      policy.Scope  `json:"scope"`
}
type ClientPolicyExplanation struct {
	Name             string                `json:"name"`
	Normalized       string                `json:"normalized"`
	Generation       string                `json:"generation"`
	ClientID         string                `json:"client_id"`
	MatchingMethod   string                `json:"matching_method"`
	AuthoritativeMAC string                `json:"authoritative_mac,omitempty"`
	Handling         string                `json:"handling"`
	Effective        EffectiveClientPolicy `json:"effective"`
	Decision         PolicyDecision        `json:"decision"`
}

func matchingMethod(c config.Config, p *config.EffectivePolicy, a netip.Addr, mac string) string {
	if p.ClientID() == "" {
		return "network"
	}
	for _, v := range c.Clients {
		if policyClientID(v) == p.ClientID() {
			for _, raw := range append(append([]string{}, v.Selectors.Addresses...), v.Address) {
				ip, e := netip.ParseAddr(raw)
				if e == nil && ip.Unmap() == a.Unmap() {
					return "address"
				}
			}
			if mac != "" {
				for _, raw := range v.Selectors.MACs {
					parsed, err := net.ParseMAC(raw)
					if err == nil && parsed.String() == mac {
						return "dhcp_mac"
					}
				}
			}
			return "cidr"
		}
	}
	return "network"
}

func (s *Service) ExplainClientPolicy(m ClientPolicyExplain) (ClientPolicyExplanation, error) {
	var result ClientPolicyExplanation
	if s.options.Store == nil {
		return result, ErrUnavailable
	}
	snap := s.options.Store.Snapshot()
	if snap == nil {
		return result, ErrUnavailable
	}
	generation := strconv.FormatUint(snap.Generation(), 10)
	if m.Generation != "" && m.Generation != generation {
		return result, config.ErrConflict
	}
	if m.ClientID != "" && m.Address != "" {
		return result, fmt.Errorf("select client_id or address, not both")
	}
	n, err := policy.NormalizeName(m.Name)
	if err != nil {
		return result, err
	}
	now := time.Now()
	p := snap.ClientPolicies().Network()
	method, mac := "network", ""
	if m.ClientID != "" {
		var ok bool
		p, ok = snap.ClientPolicies().Client(m.ClientID)
		if !ok {
			return result, NotFound
		}
		method = "configured_id"
	}
	if m.Address != "" {
		a, e := netip.ParseAddr(m.Address)
		if e != nil {
			return result, e
		}
		mac = s.authoritativeMAC(a, snap, now)
		p = snap.ClientPolicies().Select(a, mac)
		method = matchingMethod(snap.Config(), p, a, mac)
	}
	effective := effectivePolicy(snap.Config(), p, now)
	typ := uint16(1)
	if m.QType != "" {
		types := map[string]uint16{"A": 1, "AAAA": 28, "PTR": 12, "CNAME": 5, "SOA": 6, "NS": 2, "MX": 15, "TXT": 16, "SRV": 33, "HTTPS": 65, "SVCB": 64, "ANY": 255}
		var ok bool
		typ, ok = types[strings.ToUpper(m.QType)]
		if !ok {
			v, e := strconv.ParseUint(m.QType, 10, 16)
			if e != nil || v == 0 {
				return result, fmt.Errorf("invalid qtype")
			}
			typ = uint16(v)
		}
	}
	wire := make([]byte, 12)
	wire[2] = 1
	wire[5] = 1
	for _, label := range strings.Split(n.Display(), ".") {
		wire = append(wire, byte(len(label)))
		wire = append(wire, label...)
	}
	wire = append(wire, 0, 0, 0, 0, 1)
	binary.BigEndian.PutUint16(wire[len(wire)-4:], typ)
	var q dnswire.Message
	if err = dnswire.ParseRequest(wire, &q); err != nil {
		return result, err
	}
	var local bool
	out := make([]byte, 65535)
	if s.options.Leases != nil {
		leases := s.options.Leases(snap)
		if leases != nil && leases.Generation() != snap.Generation() {
			leases = nil
		}
		_, local, err = snap.Local().AnswerWithLeases(out, &q, leases, now)
	} else {
		_, local, err = snap.Local().Answer(out, &q)
	}
	if err != nil {
		return result, err
	}
	decision := p.Policy().Evaluate(policy.Query{Original: n, Name: n, Explain: true, Local: local, Paused: !effective.Filtering})
	handling := "policy"
	if local {
		handling = "local"
	} else if policy.PrivateReverse(n) {
		handling = "private_reverse"
		decision = policy.Decision{Result: policy.Block, Generation: snap.Generation()}
	}
	sources := decision.SourceIDs
	if sources == nil {
		sources = []string{}
	}
	result = ClientPolicyExplanation{Name: m.Name, Normalized: n.Display(), Generation: generation, ClientID: p.ClientID(), MatchingMethod: method, AuthoritativeMAC: mac, Handling: handling, Effective: effective, Decision: PolicyDecision{Result: decision.Result, Generation: generation, RuleID: decision.RuleID, SourceIDs: sources, Scope: decision.Scope}}
	return result, nil
}
