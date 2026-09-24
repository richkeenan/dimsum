// Package queryresult formats bounded historical DNS replies off the request path.
package queryresult

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/policy"
)

const (
	MaxWireBytes  = 4096
	MaxRecords    = 16
	MaxValueBytes = 1024
	MaxJSONBytes  = 65536
)

type Record struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Value   string `json:"value"`
	TTL     uint32 `json:"ttl"`
	Section string `json:"section"`
}

type Summary struct {
	Records   []Record `json:"records"`
	Truncated bool     `json:"truncated"`
}

// Summarize consumes an owned copy of the final reply. limited explicitly permits
// a captured prefix: complete records before the cutoff remain useful, but are
// never advertised as a complete response. Invalid full captures are unavailable.
func Summarize(wire []byte, limited bool) *Summary {
	var scanner dnswire.Scanner
	if scanner.Init(wire) != nil {
		return nil
	}
	s := &Summary{Records: []Record{}, Truncated: limited}
	var r dnswire.Record
	for scanner.Next(&r) {
		if r.Type == 41 {
			continue
		} // OPT's TTL field is protocol metadata.
		if len(s.Records) == MaxRecords {
			s.Truncated = true
			continue
		}
		value := recordValue(wire, r)
		if len(value) > MaxValueBytes {
			value = value[:MaxValueBytes-3] + "..."
			s.Truncated = true
		}
		s.Records = append(s.Records, Record{Name: displayName(r.Name), Type: typeName(r.Type), Value: value, TTL: r.TTL, Section: [...]string{"answer", "authority", "additional"}[r.Section]})
	}
	if scanner.Err() != nil && !limited {
		return nil
	}
	return s
}

func displayName(n dnswire.Name) string {
	name, _ := policy.NameFromWire(n.Canonical[:n.Length])
	if n.Length == 1 {
		return "."
	}
	return name.Display()
}

func typeName(t uint16) string {
	switch t {
	case 1:
		return "A"
	case 2:
		return "NS"
	case 5:
		return "CNAME"
	case 6:
		return "SOA"
	case 12:
		return "PTR"
	case 15:
		return "MX"
	case 16:
		return "TXT"
	case 28:
		return "AAAA"
	case 33:
		return "SRV"
	case 39:
		return "DNAME"
	case 43:
		return "DS"
	case 46:
		return "RRSIG"
	case 47:
		return "NSEC"
	case 48:
		return "DNSKEY"
	case 64:
		return "SVCB"
	case 65:
		return "HTTPS"
	case 257:
		return "CAA"
	default:
		return "TYPE" + strconv.Itoa(int(t))
	}
}

func recordValue(wire []byte, r dnswire.Record) string {
	d := r.RData
	nameAt := func(off int) (string, int) {
		var n dnswire.Name
		if dnswire.DecodeName(wire, off, &n) != nil {
			return "", off
		}
		return displayName(n), n.End
	}
	switch r.Type {
	case 1, 28:
		a, _ := netip.AddrFromSlice(d)
		return a.String()
	case 2, 5, 12, 39:
		n, _ := nameAt(r.DataOffset)
		return n
	case 15:
		n, _ := nameAt(r.DataOffset + 2)
		return fmt.Sprintf("%d %s", binary.BigEndian.Uint16(d), n)
	case 33:
		n, _ := nameAt(r.DataOffset + 6)
		return fmt.Sprintf("%d %d %d %s", binary.BigEndian.Uint16(d), binary.BigEndian.Uint16(d[2:]), binary.BigEndian.Uint16(d[4:]), n)
	case 6:
		ns, off := nameAt(r.DataOffset)
		mail, off := nameAt(off)
		return fmt.Sprintf("%s %s %d %d %d %d %d", ns, mail, binary.BigEndian.Uint32(wire[off:]), binary.BigEndian.Uint32(wire[off+4:]), binary.BigEndian.Uint32(wire[off+8:]), binary.BigEndian.Uint32(wire[off+12:]), binary.BigEndian.Uint32(wire[off+16:]))
	case 16:
		var parts []string
		for off := 0; off < len(d); {
			n := int(d[off])
			off++
			parts = append(parts, strconv.QuoteToASCII(string(d[off:off+n])))
			off += n
		}
		return strings.Join(parts, " ")
	case 64, 65:
		n, off := nameAt(r.DataOffset + 2)
		parts := []string{strconv.Itoa(int(binary.BigEndian.Uint16(d))), n}
		for off+4 <= r.End {
			key, size := binary.BigEndian.Uint16(wire[off:]), int(binary.BigEndian.Uint16(wire[off+2:]))
			off += 4
			if off+size > r.End {
				break
			}
			parts = append(parts, serviceParam(key, wire[off:off+size]))
			off += size
		}
		return strings.Join(parts, " ")
	default:
		// RFC 3597-style opaque data, never interpreted as an IP or hostname.
		value := fmt.Sprintf("\\# %d %s", len(d), hex.EncodeToString(d[:min(len(d), 512)]))
		if len(d) > 512 {
			value += "..."
		}
		return value
	}
}

func serviceParam(key uint16, d []byte) string {
	switch key {
	case 1:
		var names []string
		for off := 0; off < len(d); {
			n := int(d[off])
			off++
			if off+n > len(d) {
				break
			}
			names = append(names, strings.Trim(strconv.QuoteToASCII(string(d[off:off+n])), `"`))
			off += n
		}
		return "alpn=" + strings.Join(names, ",")
	case 2:
		return "no-default-alpn"
	case 3:
		if len(d) == 2 {
			return fmt.Sprintf("port=%d", binary.BigEndian.Uint16(d))
		}
	case 4, 6:
		size, label := 4, "ipv4hint="
		if key == 6 {
			size, label = 16, "ipv6hint="
		}
		var addresses []string
		for off := 0; off+size <= len(d); off += size {
			a, _ := netip.AddrFromSlice(d[off : off+size])
			addresses = append(addresses, a.String())
		}
		return label + strings.Join(addresses, ",")
	}
	return fmt.Sprintf("key%d=%s", key, hex.EncodeToString(d))
}
