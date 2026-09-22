package clients

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/localdns"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/richkeenan/dimsum/internal/upstream"
	"io"
	"net/netip"
	"os"
	"strings"
	"time"
)

func discover(ctx context.Context, v *View, a netip.Addr) Name {
	now := time.Now()
	n := Name{Address: a, Source: "unknown", Updated: now, Expires: now.Add(time.Minute), Negative: true, Fresh: true}
	if v.settings.HostsFile != "" {
		n.Source = "hosts"
		name, e := hostsName(v.settings.HostsFile, a)
		if e != nil {
			n.Error = e.Error()
		}
		if name != "" {
			n.Name = name
			n.Negative = false
			n.Expires = now.Add(5 * time.Minute)
			return n
		}
	}
	if v.settings.Resolver != "" {
		n.Source = "router-ptr"
		name, ttl, e := routerPTR(ctx, v.settings.Resolver, a)
		if e != nil {
			n.Error = e.Error()
			return n
		}
		if name != "" {
			n.Name = name
			n.Negative = false
			if ttl > 300 {
				ttl = 300
			}
			n.Expires = now.Add(time.Duration(ttl) * time.Second)
			return n
		}
	}
	return n
}
func hostsName(path string, a netip.Addr) (string, error) {
	info, e := os.Stat(path)
	if e != nil {
		return "", e
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return "", fmt.Errorf("hosts source must be a regular file at most 1 MiB")
	}
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	info, e = f.Stat()
	if e != nil {
		return "", e
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return "", fmt.Errorf("hosts source must be a regular file at most 1 MiB")
	}
	s := bufio.NewScanner(io.LimitReader(f, 1<<20))
	for s.Scan() {
		line := strings.SplitN(s.Text(), "#", 2)[0]
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ip, e := netip.ParseAddr(fields[0])
		if e != nil || ip.Unmap() != a {
			continue
		}
		name, e := policy.NormalizeName(fields[1])
		if e == nil {
			return name.Display(), nil
		}
	}
	return "", s.Err()
}

// This path uses exactly one explicit local endpoint. No net.DefaultResolver,
// public fallback, multicast probing, or daemon listener is consulted.
func routerPTR(ctx context.Context, endpoint string, a netip.Addr) (string, uint32, error) {
	reverse := localdns.Reverse(a)
	wire := make([]byte, 12)
	wire[2] = 1
	wire[5] = 1
	for _, label := range strings.Split(reverse, ".") {
		wire = append(wire, byte(len(label)))
		wire = append(wire, label...)
	}
	wire = append(wire, 0, 0, 12, 0, 1)
	client, e := upstream.New(upstream.Options{Endpoints: []upstream.Endpoint{upstream.PlainEndpoint(netip.MustParseAddrPort(endpoint))}, Timeout: 500 * time.Millisecond, MaxOutstanding: 1})
	if e != nil {
		return "", 0, e
	}
	defer client.Close()
	out := make([]byte, 65535)
	result, e := client.Exchange(ctx, wire, out)
	if e != nil {
		return "", 0, e
	}
	message := out[:result.N]
	var s dnswire.Scanner
	if e = s.Init(message); e != nil {
		return "", 0, e
	}
	if binary.BigEndian.Uint16(message[2:])&15 != 0 {
		return "", 0, nil
	}
	var rr dnswire.Record
	var name string
	var ttl uint32
	for s.Next(&rr) {
		owner, _ := policy.NameFromWire(rr.Name.Canonical[:rr.Name.Length])
		if rr.Section != dnswire.Answer || rr.Class != 1 || rr.Type != 12 || owner.Display() != reverse {
			continue
		}
		var target dnswire.Name
		if e = dnswire.DecodeName(message, rr.DataOffset, &target); e != nil {
			return "", 0, e
		}
		n, _ := policy.NameFromWire(target.Canonical[:target.Length])
		if _, e = policy.NormalizeName(n.Display()); e != nil {
			continue
		}
		if name == "" || n.Display() < name {
			name = n.Display()
			ttl = rr.TTL
		}
	}
	if s.Err() != nil {
		return "", 0, s.Err()
	}
	return name, ttl, nil
}
