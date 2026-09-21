package clients

import (
	"context"
	"fmt"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"net"
	"slices"
	"sync"
	"time"
)

type mdnsDatagram struct {
	iface int
	wire  []byte
	err   error
}
type mdnsTransport interface {
	Send(int, []byte) error
	Packets() <-chan mdnsDatagram
	Interfaces() []int
	Close()
}
type multicastTransport struct {
	v4               *ipv4.PacketConn
	v6               *ipv6.PacketConn
	ids              []int
	joined4, joined6 map[int]bool
	packets          chan mdnsDatagram
	cancel           context.CancelFunc
	workers          sync.WaitGroup
}

func openMDNSTransport(parent context.Context, s MDNSSettings) (mdnsTransport, []string) {
	ctx, cancel := context.WithCancel(parent)
	t := &multicastTransport{packets: make(chan mdnsDatagram, 128), cancel: cancel, joined4: map[int]bool{}, joined6: map[int]bool{}}
	interfaces, err := net.Interfaces()
	if err != nil {
		cancel()
		return nil, []string{err.Error()}
	}
	var selected []net.Interface
	var problems []string
	for _, i := range interfaces {
		if len(s.Interfaces) > 0 && !slices.Contains(s.Interfaces, i.Name) {
			continue
		}
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagMulticast == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(selected) < 8 {
			selected = append(selected, i)
		}
	}
	for _, name := range s.Interfaces {
		if !slices.ContainsFunc(selected, func(i net.Interface) bool { return i.Name == name }) {
			problems = append(problems, "Interface unavailable: "+name)
		}
	}
	if len(selected) == 0 {
		cancel()
		return nil, append(problems, "No eligible multicast interfaces")
	}
	// Go's multicast-address ListenPacket path enables socket reuse before binding.
	if c, e := net.ListenPacket("udp4", "224.0.0.251:5353"); e == nil {
		p := ipv4.NewPacketConn(c)
		if e = p.SetControlMessage(ipv4.FlagInterface|ipv4.FlagTTL|ipv4.FlagDst, true); e == nil {
			e = p.SetMulticastTTL(255)
		}
		if e == nil {
			e = p.SetMulticastLoopback(true)
		}
		if e != nil {
			c.Close()
			problems = append(problems, "IPv4 multicast: "+e.Error())
		} else {
			t.v4 = p
		}
	} else {
		problems = append(problems, "IPv4 multicast: "+e.Error())
	}
	if c, e := net.ListenPacket("udp6", "[ff02::fb]:5353"); e == nil {
		p := ipv6.NewPacketConn(c)
		if e = p.SetControlMessage(ipv6.FlagInterface|ipv6.FlagHopLimit|ipv6.FlagDst, true); e == nil {
			e = p.SetMulticastHopLimit(255)
		}
		if e == nil {
			e = p.SetMulticastLoopback(true)
		}
		if e != nil {
			c.Close()
			problems = append(problems, "IPv6 multicast: "+e.Error())
		} else {
			t.v6 = p
		}
	} else {
		problems = append(problems, "IPv6 multicast: "+e.Error())
	}
	for _, i := range selected {
		if t.v4 != nil {
			if e := t.v4.JoinGroup(&i, &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251)}); e == nil {
				t.joined4[i.Index] = true
			} else {
				problems = append(problems, i.Name+" IPv4: "+e.Error())
			}
		}
		if t.v6 != nil {
			if e := t.v6.JoinGroup(&i, &net.UDPAddr{IP: net.ParseIP("ff02::fb")}); e == nil {
				t.joined6[i.Index] = true
			} else {
				problems = append(problems, i.Name+" IPv6: "+e.Error())
			}
		}
		if t.joined4[i.Index] || t.joined6[i.Index] {
			t.ids = append(t.ids, i.Index)
		}
	}
	if len(t.ids) == 0 {
		t.Close()
		return nil, problems
	}
	if t.v4 != nil {
		t.workers.Add(1)
		go func() { defer t.workers.Done(); t.read(ctx, false) }()
	}
	if t.v6 != nil {
		t.workers.Add(1)
		go func() { defer t.workers.Done(); t.read(ctx, true) }()
	}
	return t, problems
}
func (t *multicastTransport) Packets() <-chan mdnsDatagram { return t.packets }
func (t *multicastTransport) Interfaces() []int            { return t.ids }
func (t *multicastTransport) Close() {
	t.cancel()
	if t.v4 != nil {
		t.v4.Close()
	}
	if t.v6 != nil {
		t.v6.Close()
	}
	t.workers.Wait()
}
func (t *multicastTransport) read(ctx context.Context, v6 bool) {
	buf := make([]byte, maxMDNSPacket+1)
	for {
		var n, iface, hops int
		var src net.Addr
		var err error
		if v6 {
			var cm *ipv6.ControlMessage
			n, cm, src, err = t.v6.ReadFrom(buf)
			if cm != nil {
				iface, hops = cm.IfIndex, cm.HopLimit
			}
		} else {
			var cm *ipv4.ControlMessage
			n, cm, src, err = t.v4.ReadFrom(buf)
			if cm != nil {
				iface, hops = cm.IfIndex, cm.TTL
			}
		}
		if err != nil {
			if ctx.Err() == nil {
				select {
				case t.packets <- mdnsDatagram{err: err}:
				default:
				}
			}
			return
		}
		sender, ok := src.(*net.UDPAddr)
		if !ok || sender.Port != 5353 || hops != 255 || n > maxMDNSPacket || (!t.joined4[iface] && !v6) || (!t.joined6[iface] && v6) {
			continue
		}
		if sender.IP.IsUnspecified() || sender.IP.IsMulticast() {
			continue
		}
		select {
		case t.packets <- mdnsDatagram{iface: iface, wire: append([]byte(nil), buf[:n]...)}:
		case <-ctx.Done():
			return
		default:
		}
	}
}
func (t *multicastTransport) Send(iface int, p []byte) error {
	var err4, err6 error
	if t.joined4[iface] {
		t.v4.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
		_, err4 = t.v4.WriteTo(p, &ipv4.ControlMessage{IfIndex: iface}, &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353})
	}
	if t.joined6[iface] {
		t.v6.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
		_, err6 = t.v6.WriteTo(p, &ipv6.ControlMessage{IfIndex: iface}, &net.UDPAddr{IP: net.ParseIP("ff02::fb"), Port: 5353})
	}
	if err4 != nil || err6 != nil {
		return fmt.Errorf("multicast send: IPv4=%v IPv6=%v", err4, err6)
	}
	return nil
}
