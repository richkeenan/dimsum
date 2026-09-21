package policy

import (
	"encoding/binary"
	"fmt"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"strings"
)

type ResponseDecision struct {
	Decision               Decision
	Original, BlockedAlias Name
	Links                  int
}

// InspectResponse follows only answer-section aliases reachable from the original
// name. Validation and the 16-link bound apply even to allowed/paused requests.
// ServiceMode parameters and unrelated additional records are not name policy.
func (p *PolicySnapshot) InspectResponse(message []byte, original Name, paused bool) (ResponseDecision, error) {
	result := ResponseDecision{Original: original, Decision: Decision{Result: Forward, Generation: p.generation}}
	var envelope dnswire.Message
	if e := dnswire.ScanMessage(message, &envelope); e != nil {
		return result, e
	}
	current := original
	seen := map[Name]bool{current: true}
	for {
		var scan dnswire.Scanner
		if e := scan.Init(message); e != nil {
			return result, e
		}
		var rr dnswire.Record
		var direct, dname Name
		haveDirect, haveDNAME := false, false
		dnameOwnerLen := -1
		for scan.Next(&rr) {
			if rr.Section != dnswire.Answer || rr.Class != 1 {
				continue
			}
			owner, _ := NameFromWire(rr.Name.Canonical[:rr.Name.Length])
			off := rr.DataOffset
			relevant := rr.Type == 5 && owner == current
			if (rr.Type == 64 || rr.Type == 65) && rr.Type == envelope.Question.Type && owner == current && binary.BigEndian.Uint16(rr.RData) == 0 {
				relevant = true
				off += 2
			}
			ancestor := rr.Type == 39 && owner != current && wireSuffix(current, owner)
			if !relevant && !ancestor {
				continue
			}
			var decoded dnswire.Name
			if e := dnswire.DecodeName(message, off, &decoded); e != nil {
				return result, e
			}
			target, e := NameFromWire(decoded.Canonical[:decoded.Length])
			if e != nil {
				return result, e
			}
			if relevant {
				if haveDirect && direct != target {
					return result, fmt.Errorf("policy: conflicting aliases")
				}
				direct = target
				haveDirect = true
			}
			if ancestor {
				synth := current.wire[:len(current.wire)-len(owner.wire)] + target.wire
				if len(synth)+1 > 255 {
					return result, fmt.Errorf("policy: DNAME name too long")
				}
				if len(owner.wire) > dnameOwnerLen {
					dname = Name{wire: synth}
					haveDNAME = true
					dnameOwnerLen = len(owner.wire)
				} else if len(owner.wire) == dnameOwnerLen && dname.wire != synth {
					return result, fmt.Errorf("policy: conflicting DNAME")
				}
			}
		}
		if e := scan.Err(); e != nil {
			return result, e
		}
		if !haveDirect && !haveDNAME {
			return result, nil
		}
		if haveDNAME {
			if haveDirect && direct != dname {
				return result, fmt.Errorf("policy: inconsistent synthesized CNAME")
			}
			direct = dname
		}
		result.Links++
		if result.Links > 16 || seen[direct] {
			return result, fmt.Errorf("policy: alias cycle or depth limit")
		}
		seen[direct] = true
		current = direct
		d := p.Evaluate(Query{Original: original, Name: current, Paused: paused})
		if d.Result == Block && result.Decision.Result != Block {
			result.Decision = d
			result.BlockedAlias = current
		}
	}
}
func wireSuffix(name, suffix Name) bool {
	for i := 0; i < len(name.wire); i += 1 + int(name.wire[i]) {
		if name.wire[i:] == suffix.wire {
			return true
		}
	}
	return suffix.wire == ""
}

// PrivateReverse protects client-query routing independently of ad-filter pause.
// Match reverse-zone suffixes as well as full PTR names (including unknown QTYPEs).
func PrivateReverse(n Name) bool {
	s := n.Display()
	for _, zone := range []string{"10.in-addr.arpa", "127.in-addr.arpa", "168.192.in-addr.arpa", "254.169.in-addr.arpa", "0.in-addr.arpa", "c.f.ip6.arpa", "d.f.ip6.arpa", "8.e.f.ip6.arpa", "9.e.f.ip6.arpa", "a.e.f.ip6.arpa", "b.e.f.ip6.arpa"} {
		if s == zone || strings.HasSuffix(s, "."+zone) {
			return true
		}
	}
	for i := 16; i <= 31; i++ {
		zone := fmt.Sprintf("%d.172.in-addr.arpa", i)
		if s == zone || strings.HasSuffix(s, "."+zone) {
			return true
		}
	}
	return s == "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.ip6.arpa" || s == "0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.ip6.arpa"
}
