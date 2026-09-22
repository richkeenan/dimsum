//go:build linux

package dhcp

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mdlayher/packet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestLinuxIndependentClientAvoidsARPConflict(t *testing.T) {
	if os.Getenv("DIMSUM_DHCP_UDHCPC_TEST") != "1" {
		t.Skip("requires isolated BusyBox fixture")
	}
	isolatedLink(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	iface, err := net.InterfaceByName("dhcp-client")
	require.NoError(t, err)
	arp, err := packet.Listen(iface, packet.Datagram, unix.ETH_P_ARP, nil)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		var buf [128]byte
		for {
			_ = arp.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, _, e := arp.ReadFrom(buf[:])
			if ctx.Err() != nil {
				done <- nil
				return
			}
			if e != nil {
				if timeout, ok := e.(net.Error); ok && timeout.Timeout() {
					continue
				}
				done <- e
				return
			}
			if n < 28 || !bytes.Equal(buf[24:28], []byte{192, 0, 2, 100}) {
				continue
			}
			claim := []byte{0, 1, 8, 0, 6, 4, 0, 2, 2, 0, 0, 0, 0, 99, 192, 0, 2, 100, 2, 0, 0, 0, 0, 1, 0, 0, 0, 0}
			if _, e = arp.WriteTo(claim, &packet.Addr{HardwareAddr: net.HardwareAddr{2, 0, 0, 0, 0, 1}}); e != nil {
				done <- e
				return
			}
		}
	}()
	defer func() { cancel(); arp.Close(); require.NoError(t, <-done) }()
	s := fixtureSettings()
	s.Interface = "dhcp-server"
	link, probe, err := OpenSystemLink(s)
	require.NoError(t, err)
	dir := t.TempDir()
	store, rows, err := OpenLeaseStore(filepath.Join(dir, "dhcp"), 1024, nil)
	require.NoError(t, err)
	rt, err := StartRuntime(ctx, s, 1, link, probe, store, rows)
	require.NoError(t, err)
	defer rt.Close(context.Background())
	script := filepath.Join(dir, "script")
	result := filepath.Join(dir, "result")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nif [ \"$1\" = bound ]; then echo \"$ip\" > \"$DIMSUM_CLIENT_RESULT\"; fi\n"), 0700))
	cmd := exec.CommandContext(ctx, "busybox", "udhcpc", "-f", "-n", "-q", "-t", "3", "-T", "1", "-i", "dhcp-client", "-s", script)
	cmd.Env = append(os.Environ(), "DIMSUM_CLIENT_RESULT="+result)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	got, err := os.ReadFile(result)
	require.NoError(t, err)
	assert.Equal(t, "192.0.2.101\n", string(got))
	require.NoError(t, rt.Close(context.Background()))
	reopened, recovery, err := OpenLeaseStore(filepath.Join(dir, "dhcp"), 1024, nil)
	require.NoError(t, err)
	defer reopened.Close(context.Background())
	require.Len(t, recovery.Leases, 2)
	states := map[netip.Addr]LeaseState{}
	for _, l := range recovery.Leases {
		states[l.Address] = l.State
	}
	assert.Equal(t, Quarantined, states[netip.MustParseAddr("192.0.2.100")])
	assert.Equal(t, Bound, states[netip.MustParseAddr("192.0.2.101")])
	t.Logf("independent client skipped conflicting address, durable quarantine retained:\n%s", out)
}

// Qualifies the diagnostic's real UDP/68 broadcast adapter against the real
// server, exclusively in the explicitly opted-in disposable veth fixture.
func TestLinuxDiagnosticBroadcast(t *testing.T) {
	isolatedLink(t)
	out, err := exec.Command("ip", "addr", "add", "192.0.2.3/24", "dev", "dhcp-client").CombinedOutput()
	require.NoError(t, err, "%s", out)
	// Both veth ends live in this disposable namespace. Permit a local source
	// arriving over the peer; a real two-host link does not need this fixture knob.
	// Run with --sysctl net.ipv4.conf.default.accept_local=1 and rp_filter=0.
	for _, setting := range []struct{ path, want string }{
		{"all/rp_filter", "0"},
		{"dhcp-server/rp_filter", "0"},
		{"dhcp-client/rp_filter", "0"},
		{"dhcp-server/accept_local", "1"},
		{"dhcp-client/accept_local", "1"},
	} {
		value, err := os.ReadFile("/proc/sys/net/ipv4/conf/" + setting.path)
		require.NoError(t, err)
		require.Equal(t, setting.want, strings.TrimSpace(string(value)),
			"isolated diagnostic fixture needs %s=%s; run scripts/dhcp-qualification.py or set the documented container sysctls", setting.path, setting.want)
	}
	s := fixtureSettings()
	s.Interface = "dhcp-server"
	link, probe, err := OpenSystemLink(s)
	require.NoError(t, err)
	store, rows, err := OpenLeaseStore(filepath.Join(t.TempDir(), "dhcp"), 1024, nil)
	require.NoError(t, err)
	rt, err := StartRuntime(context.Background(), s, 1, link, probe, store, rows)
	require.NoError(t, err)
	defer rt.Close(context.Background())
	client := s
	client.Interface = "dhcp-client"
	client.ServerIP = "192.0.2.3"
	result, err := ProbeServers(context.Background(), client, 3*time.Second)
	require.NoError(t, err)
	assert.Equal(t, []string{"192.0.2.2"}, result.Servers)
	assert.False(t, result.Truncated)
	for _, lease := range rt.Leases() {
		assert.Equal(t, Offered, lease.State, "diagnostic must never REQUEST or acquire a durable lease")
	}
	assert.EqualValues(t, 1, rt.Status().Transport.Received)
	// The exclusive diagnostic socket must be released on return.
	conn, err := openDiagnosticSocket(client.Interface)
	require.NoError(t, err)
	assert.NoError(t, conn.Close())
	t.Logf("real broadcast diagnostic: %+v; runtime %+v", result, rt.Status())
}

// The same test binary is the server child, so both restarts execute the real
// production socket/runtime/store code in separate OS processes.
func TestLinuxRuntimeProcessRestart(t *testing.T) {
	if dir := os.Getenv("DIMSUM_DHCP_SERVER_CHILD"); dir != "" {
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		defer cancel()
		s := fixtureSettings()
		s.Interface = "dhcp-server"
		link, probe, err := OpenSystemLink(s)
		require.NoError(t, err)
		store, rows, err := OpenLeaseStore(filepath.Join(dir, "dhcp"), 1024, nil)
		require.NoError(t, err)
		rt, err := StartRuntime(ctx, s, 1, link, probe, store, rows)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0600))
		<-ctx.Done()
		require.NoError(t, rt.Close(context.Background()))
		return
	}
	if os.Getenv("DIMSUM_DHCP_UDHCPC_TEST") != "1" {
		t.Skip("requires isolated BusyBox fixture")
	}
	isolatedLink(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "script")
	result := filepath.Join(dir, "result")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nif [ \"$1\" = bound ]; then echo \"$ip $serverid\" > \"$DIMSUM_CLIENT_RESULT\"; fi\n"), 0700))
	previous := 0
	for attempt := 0; attempt < 2; attempt++ {
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLinuxRuntimeProcessRestart$", "-test.v")
			cmd.Env = append(os.Environ(), "DIMSUM_DHCP_SERVER_CHILD="+dir)
			var output bytes.Buffer
			cmd.Stdout = &output
			cmd.Stderr = &output
			require.NoError(t, cmd.Start())
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			assert.NotEqual(t, previous, cmd.Process.Pid)
			previous = cmd.Process.Pid
			require.Eventually(t, func() bool { _, e := os.Stat(filepath.Join(dir, "ready")); return e == nil }, 3*time.Second, time.Millisecond)
			client := exec.CommandContext(ctx, "busybox", "udhcpc", "-f", "-n", "-q", "-t", "3", "-T", "1", "-i", "dhcp-client", "-s", script)
			client.Env = append(os.Environ(), "DIMSUM_CLIENT_RESULT="+result)
			b, err := client.CombinedOutput()
			require.NoError(t, err, "%s", b)
			got, err := os.ReadFile(result)
			require.NoError(t, err)
			assert.Equal(t, "192.0.2.100 192.0.2.2\n", string(got))
			require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
			require.NoError(t, cmd.Wait(), "%s", output.String())
			require.NoError(t, os.Remove(filepath.Join(dir, "ready")))
			require.NoError(t, os.Remove(result))
			t.Logf("server process %d attempt %d; independent client:\n%s", previous, attempt, b)
		}()
	}
	db, recovered, err := OpenLeaseStore(filepath.Join(dir, "dhcp"), 1024, nil)
	require.NoError(t, err)
	defer db.Close(context.Background())
	require.Len(t, recovered.Leases, 1)
	assert.Equal(t, netip.MustParseAddr("192.0.2.100"), recovered.Leases[0].Address)
}

func TestLinuxIndependentClientRenewRestart(t *testing.T) {
	if os.Getenv("DIMSUM_DHCP_NETNS_TEST") != "1" {
		t.Skip("requires disposable container with SYS_ADMIN and unconfined seccomp for child netns")
	}
	isolatedLink(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ns := exec.CommandContext(ctx, "unshare", "--net", "sleep", "90")
	require.NoError(t, ns.Start())
	defer func() { _ = ns.Process.Kill(); _ = ns.Wait() }()
	pid := fmt.Sprint(ns.Process.Pid)
	// Wait for unshare to replace the inherited network namespace.
	parent, err := os.Readlink("/proc/self/ns/net")
	require.NoError(t, err)
	require.Eventually(t, func() bool { child, e := os.Readlink("/proc/" + pid + "/ns/net"); return e == nil && child != parent }, time.Second, time.Millisecond)
	out, err := exec.Command("ip", "link", "set", "dhcp-client", "netns", pid).CombinedOutput()
	require.NoError(t, err, "%s", out)
	run := func(args ...string) {
		t.Helper()
		out, e := exec.Command("nsenter", append([]string{"-t", pid, "-n"}, args...)...).CombinedOutput()
		require.NoError(t, e, "%s", out)
	}
	run("ip", "link", "set", "lo", "up")
	run("ip", "link", "set", "dhcp-client", "up")
	s := fixtureSettings()
	s.Interface = "dhcp-server"
	s.LeaseSeconds = 60
	dir := t.TempDir()
	var activeLink *systemLink
	start := func() *Runtime {
		t.Helper()
		link, probe, e := OpenSystemLink(s)
		require.NoError(t, e)
		activeLink = link.(*systemLink)
		store, rows, e := OpenLeaseStore(filepath.Join(dir, "dhcp"), 1024, nil)
		require.NoError(t, e)
		rt, e := StartRuntime(ctx, s, 1, link, probe, store, rows)
		require.NoError(t, e)
		return rt
	}
	rt := start()
	defer func() { _ = rt.Close(context.Background()) }()
	result := filepath.Join(dir, "events")
	script := filepath.Join(dir, "script")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\ncase \"$1\" in bound|renew) ip addr replace \"$ip/24\" dev dhcp-client; echo \"$1 $ip $serverid\" >> \"$DIMSUM_CLIENT_RESULT\";; esac\n"), 0700))
	log, err := os.Create(filepath.Join(dir, "client.log"))
	require.NoError(t, err)
	defer log.Close()
	cmd := exec.CommandContext(ctx, "nsenter", "-t", pid, "-n", "busybox", "udhcpc", "-f", "-n", "-i", "dhcp-client", "-s", script, "-t", "3", "-T", "1")
	cmd.Env = append(os.Environ(), "DIMSUM_CLIENT_RESULT="+result)
	cmd.Stdout = log
	cmd.Stderr = log
	started := time.Now()
	require.NoError(t, cmd.Start())
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		b, _ := os.ReadFile(log.Name())
		t.Logf("independent client log:\n%s", b)
	}()
	events := func() string { b, _ := os.ReadFile(result); return string(b) }
	require.Eventually(t, func() bool { return strings.Contains(events(), "bound 192.0.2.100 192.0.2.2") }, 3*time.Second, time.Millisecond)
	acquired := time.Since(started)
	// The Eventually bound above includes the repeated conflict check and
	// client handshake; a fresh lease must not bypass the observation window.
	assert.GreaterOrEqual(t, acquired, 1500*time.Millisecond)
	old := rt.Leases()[0].Expiry
	time.Sleep(1100 * time.Millisecond)
	require.NoError(t, cmd.Process.Signal(syscall.SIGUSR1))
	require.Eventually(t, func() bool { return strings.Contains(events(), "renew ") }, 3*time.Second, time.Millisecond)
	assert.True(t, rt.Leases()[0].Expiry.After(old))
	require.NoError(t, rt.Close(context.Background()))
	rt = start()
	before := strings.Count(events(), "renew ")
	require.NoError(t, cmd.Process.Signal(syscall.SIGUSR1))
	require.Eventually(t, func() bool { return strings.Count(events(), "renew ") > before }, 3*time.Second, time.Millisecond)
	assert.Equal(t, netip.MustParseAddr("192.0.2.100"), rt.Leases()[0].Address)
	// Fixture-only classic socket filter: accept Ethernet broadcast packets,
	// reject unicast renewals. SKF_AD_PKTTYPE is ancillary skb metadata, so this
	// does not depend on UDP socket header offsets or optional tc classifiers.
	filter := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0xfffff004},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: unix.PACKET_BROADCAST, Jf: 1},
		{Code: unix.BPF_RET | unix.BPF_K, K: 65535},
		{Code: unix.BPF_RET | unix.BPF_K, K: 0},
	}
	raw, err := activeLink.udp.SyscallConn()
	require.NoError(t, err)
	var attachErr error
	require.NoError(t, raw.Control(func(fd uintptr) {
		attachErr = unix.SetsockoptSockFprog(int(fd), unix.SOL_SOCKET, unix.SO_ATTACH_FILTER, &unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]})
	}))
	require.NoError(t, attachErr)
	before = strings.Count(events(), "renew ")
	rebindStart := time.Now()
	require.Eventually(t, func() bool { return strings.Count(events(), "renew ") > before }, 65*time.Second, 20*time.Millisecond)
	assert.Equal(t, 1, strings.Count(events(), "bound "), "rebinding should not require another acquisition")
	t.Logf("natural broadcast rebind=%s with fixture UDP socket accepting only PACKET_BROADCAST", time.Since(rebindStart))
	// Query the independent client's configured interface, not the server's view.
	out, err = exec.Command("nsenter", "-t", pid, "-n", "ip", "-4", "addr", "show", "dev", "dhcp-client").CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(out), "192.0.2.100/24")
	t.Logf("DISCOVER-to-bound=%s; independent unicast renewal and server runtime/store restart succeeded; %s", acquired, events())
}
