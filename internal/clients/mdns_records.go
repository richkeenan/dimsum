package clients

import (
	"encoding/binary"
	"fmt"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/policy"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxMDNSPacket = 9000
const maxMDNSRecords = 8192

type recordKey struct {
	iface int
	owner string
	kind  uint16
	data  string
}
type mdnsRecord struct {
	key              recordKey
	target           string
	address          netip.Addr
	txt              map[string]string
	label            string
	learned, expires time.Time
	flush            bool
}
type mdnsCache struct{ records map[recordKey]mdnsRecord }

func newMDNSCache() *mdnsCache { return &mdnsCache{records: make(map[recordKey]mdnsRecord)} }
func dnsName(n dnswire.Name) string {
	v, _ := policy.NameFromWire(n.Canonical[:n.Length])
	return v.Display()
}
func safeLabel(s string) string {
	if len(s) > 256 || !utf8.ValidString(s) {
		return ""
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return strings.TrimSpace(s)
}
func (c *mdnsCache) ingest(ifindex int, p []byte, now time.Time) error {
	if ifindex <= 0 || len(p) > maxMDNSPacket {
		return fmt.Errorf("invalid multicast packet/interface")
	}
	h, err := dnswire.ParseHeader(p)
	if err != nil {
		return err
	}
	if h.ID != 0 || h.Flags&0x8000 == 0 || h.Flags&0x7a0f != 0 || int(h.Answers)+int(h.Authorities)+int(h.Additionals) > 128 {
		return fmt.Errorf("invalid multicast response")
	}
	var scanner dnswire.Scanner
	if err = scanner.InitMulticast(p); err != nil {
		return err
	}
	batch := make([]mdnsRecord, 0, 16)
	var rr dnswire.Record
	for scanner.Next(&rr) {
		if rr.Class&0x7fff != 1 {
			continue
		}
		r := mdnsRecord{key: recordKey{iface: ifindex, owner: dnsName(rr.Name), kind: rr.Type, data: string(rr.RData)}, learned: now, expires: now.Add(time.Duration(min(rr.TTL, 300)) * time.Second), flush: rr.Class&0x8000 != 0}
		switch rr.Type {
		case 1, 28:
			a, ok := netip.AddrFromSlice(rr.RData)
			if !ok {
				return fmt.Errorf("bad address")
			}
			r.address = a.Unmap()
		case 12, 33:
			off := rr.DataOffset
			if rr.Type == 33 {
				off += 6
			}
			var target dnswire.Name
			if err = dnswire.DecodeName(p, off, &target); err != nil {
				return err
			}
			if target.End != rr.End {
				return fmt.Errorf("trailing target data")
			}
			r.target = dnsName(target)
			r.key.data = r.target
			if rr.Type == 33 {
				r.key.data = string(rr.RData[:6]) + r.target
			}
			if rr.Type == 12 && target.Length > 1 {
				r.label = safeLabel(string(target.Wire[1 : 1+int(target.Wire[0])]))
			}
		case 16:
			if len(rr.RData) > 1024 {
				continue
			}
			r.txt = make(map[string]string)
			for off := 0; off < len(rr.RData); {
				n := int(rr.RData[off])
				off++
				if off+n > len(rr.RData) {
					return fmt.Errorf("bad TXT length")
				}
				k, v, ok := strings.Cut(string(rr.RData[off:off+n]), "=")
				off += n
				k = strings.ToLower(k)
				if ok {
					switch k {
					case "model", "md", "manufacturer", "ty", "product", "fn", "am", "device_type", "type":
						if _, exists := r.txt[k]; !exists {
							r.txt[k] = safeLabel(v)
						}
					}
				}
			}
		default:
			continue
		}
		batch = append(batch, r)
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	c.expire(now)
	for _, r := range batch {
		if !r.expires.After(now) {
			if old, ok := c.records[r.key]; ok {
				old.expires = minTime(old.expires, now.Add(time.Second))
				c.records[r.key] = old
			}
			continue
		}
		if r.flush {
			for k, old := range c.records {
				if k.iface == r.key.iface && k.owner == r.key.owner && k.kind == r.key.kind && k != r.key && now.Sub(old.learned) >= time.Second {
					old.expires = minTime(old.expires, now.Add(time.Second))
					c.records[k] = old
				}
			}
		}
		if _, ok := c.records[r.key]; !ok && len(c.records) >= maxMDNSRecords {
			var oldest recordKey
			var stamp time.Time
			for k, v := range c.records {
				if stamp.IsZero() || v.learned.Before(stamp) {
					oldest, stamp = k, v.learned
				}
			}
			delete(c.records, oldest)
		}
		c.records[r.key] = r
	}
	return nil
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func (c *mdnsCache) expire(now time.Time) {
	for k, r := range c.records {
		if !now.Before(r.expires) {
			delete(c.records, k)
		}
	}
}

// Query packets share the existing name normalizer/encoder rather than a DNS library.
func encodeMDNSQuery(name string, kind uint16) []byte {
	n, err := policy.NormalizeName(name)
	if err != nil {
		return nil
	}
	p := make([]byte, 12)
	p[5] = 1
	var wire [255]byte
	size := n.CopyWire(wire[:])
	p = append(p, wire[:size]...)
	p = binary.BigEndian.AppendUint16(p, kind)
	return binary.BigEndian.AppendUint16(p, 1)
}
