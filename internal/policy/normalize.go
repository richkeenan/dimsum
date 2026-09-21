package policy

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// Name is an immutable, ASCII-case-folded, byte-safe DNS name. Its zero value
// is the root. Configuration hostnames and decoded wire names use separate
// constructors: IDNA must never be applied to arbitrary wire bytes.
type Name struct{ wire string }

var hostnameProfile = idna.New(idna.MapForLookup(), idna.Transitional(false),
	idna.StrictDomainName(true), idna.ValidateLabels(true), idna.BidiRule(), idna.VerifyDNSLength(true))

// Used only after the narrow per-label checks below. Disabling STD3 permits
// underscores. Interior hyphens in underscore labels are DNS-safe; strict
// per-label checks enforce ordinary IDNA/A-label and underscore edge-hyphen rules.
// Whole-domain Bidi and length validation remain enabled.
var underscoreDomainProfile = idna.New(idna.MapForLookup(), idna.Transitional(false),
	idna.StrictDomainName(false), idna.ValidateLabels(true), idna.CheckHyphens(false), idna.BidiRule(), idna.VerifyDNSLength(true))

// NormalizeName validates a configuration DNS name. ASCII underscore labels use
// the DNS-safe letters/digits/hyphen/underscore alphabet; other labels retain
// strict nontransitional IDNA validation (including all xn-- A-labels).
// Mixed Unicode/underscore labels are rejected. One terminal root dot is removed.
func NormalizeName(s string) (Name, error) {
	if !utf8.ValidString(s) {
		return Name{}, fmt.Errorf("policy: invalid UTF-8 name")
	}
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\u3002', '\uff0e', '\uff61':
			return '.'
		}
		return r
	}, s)
	s = strings.TrimSuffix(s, ".")
	if s == "" {
		return Name{}, fmt.Errorf("policy: empty configuration hostname")
	}
	var wire []byte
	for _, label := range strings.Split(s, ".") {
		if strings.Contains(label, "_") && !strings.HasPrefix(strings.ToLower(label), "xn--") {
			if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return Name{}, fmt.Errorf("policy: invalid underscore label hyphen")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
					return Name{}, fmt.Errorf("policy: invalid underscore label %q", label)
				}
			}
		} else {
			ascii, err := hostnameProfile.ToASCII(label)
			if err != nil {
				return Name{}, fmt.Errorf("policy: invalid hostname label %q: %w", label, err)
			}
			label = ascii
		}
		if len(label) == 0 || len(label) > 63 {
			return Name{}, fmt.Errorf("policy: invalid label length")
		}
		wire = append(wire, byte(len(label)))
		wire = append(wire, label...)
	}
	// Bidi is domain-wide: an RTL label (including a decoded A-label) requires
	// every label to satisfy the Bidi rule. Per-label ToASCII cannot enforce it.
	profile := hostnameProfile
	if strings.Contains(s, "_") {
		profile = underscoreDomainProfile
	}
	if _, err := profile.ToASCII(s); err != nil {
		return Name{}, fmt.Errorf("policy: invalid DNS name %q: %w", s, err)
	}
	wire = append(wire, 0)
	return NameFromWire(wire)
}

// NameFromWire copies one complete uncompressed length-prefixed name, including
// its root byte. Pass dnswire.Name.Canonical[:Length] or Wire[:Length]; only ASCII
// A-Z is folded. Compression pointers, trailing bytes and invalid lengths fail.
func NameFromWire(b []byte) (Name, error) {
	if len(b) == 0 || len(b) > 255 {
		return Name{}, fmt.Errorf("policy: invalid wire name length")
	}
	out := append([]byte(nil), b...)
	for i := 0; i < len(out); {
		length := int(out[i])
		if length == 0 {
			if i != len(out)-1 {
				break
			}
			return Name{wire: string(out[:i])}, nil
		}
		if length > 63 || i+1+length >= len(out) {
			break
		}
		for j := i + 1; j < i+1+length; j++ {
			if out[j] >= 'A' && out[j] <= 'Z' {
				out[j] += 'a' - 'A'
			}
		}
		i += length + 1
	}
	return Name{}, fmt.Errorf("policy: malformed uncompressed name")
}

// Display returns the lowercase presentation without a terminal dot (root is
// empty). Only letters, digits, hyphen and underscore are emitted literally;
// all other label octets use unambiguous three-digit decimal escapes. Dots
// emitted literally are exclusively label separators. Regex sees this string.
func (n Name) Display() string {
	var b strings.Builder
	for i := 0; i < len(n.wire); {
		if i > 0 {
			b.WriteByte('.')
		}
		end := i + 1 + int(n.wire[i])
		for j := i + 1; j < end; j++ {
			c := n.wire[j]
			if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
				b.WriteByte(c)
			} else {
				b.WriteByte('\\')
				b.WriteByte('0' + c/100)
				b.WriteByte('0' + c/10%10)
				b.WriteByte('0' + c%10)
			}
		}
		i = end
	}
	return b.String()
}

func (n Name) labels() []string {
	var labels []string
	for i := 0; i < len(n.wire); {
		end := i + 1 + int(n.wire[i])
		labels = append(labels, n.wire[i+1:end])
		i = end
	}
	return labels
}
