// Package localdns compiles immutable local records and owned zones.
package localdns

import (
	"fmt"
	"github.com/richkeenan/dimsum/internal/policy"
	"net/netip"
	"strings"
)

type Record struct {
	Name    string `yaml:"name"`
	Type    string `yaml:"type"`
	Value   string `yaml:"value"`
	TTL     uint32 `yaml:"ttl"`
	AutoPTR bool   `yaml:"auto_ptr,omitempty"`
}
type Zone struct {
	Name        string `yaml:"name"`
	NegativeTTL uint32 `yaml:"negative_ttl"`
}

func normalized(s string) (string, error) { n, e := policy.NormalizeName(s); return n.Display(), e }
func wire(s string) []byte {
	if s == "" {
		return []byte{0}
	}
	var b []byte
	for _, l := range strings.Split(s, ".") {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	return append(b, 0)
}

func Reverse(a netip.Addr) string {
	a = a.Unmap()
	if a.Is4() {
		b := a.As4()
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa", b[3], b[2], b[1], b[0])
	}
	b := a.As16()
	const hex = "0123456789abcdef"
	var s strings.Builder
	for i := 15; i >= 0; i-- {
		s.WriteByte(hex[b[i]&15])
		s.WriteByte('.')
		s.WriteByte(hex[b[i]>>4])
		s.WriteByte('.')
	}
	s.WriteString("ip6.arpa")
	return s.String()
}
