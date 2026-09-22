//go:build darwin || linux

package app

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/control"
	"github.com/richkeenan/dimsum/internal/dhcp"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/resolve"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type qualificationLink struct {
	appDHCPPacketLink
	acks   atomic.Int64
	offers chan dhcp.WireReply
}

func (l *qualificationLink) Send(ctx context.Context, w dhcp.WireReply) error {
	p, err := dhcpv4.FromBytes(w.Payload)
	if err != nil {
		return err
	}
	if p.MessageType() == dhcpv4.MessageTypeAck {
		l.acks.Add(1)
	}
	if p.MessageType() == dhcpv4.MessageTypeOffer {
		select {
		case l.offers <- w:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// A fixed single worker delays admission to real SQLite or injects a definite
// failure. Neither mode pretends to cancel a kernel fsync.
type qualificationWriter struct {
	dhcp.LeaseWriter
	fail atomic.Bool
}

func (w *qualificationWriter) Submit(ctx context.Context, m dhcp.Mutation) error {
	if w.fail.Load() {
		return errors.New("qualification: definite storage admission failure")
	}
	return w.LeaseWriter.Submit(ctx, m)
}

func (w *qualificationWriter) Err() error {
	if w.fail.Load() {
		return errors.New("qualification: definite storage failure")
	}
	return w.LeaseWriter.Err()
}

type qualificationSample struct {
	Label                         string  `json:"label"`
	Queries                       int     `json:"queries"`
	QPS                           float64 `json:"qps"`
	P50NS, P95NS, P99NS           int64
	Heap, RSS, Allocated, PauseNS uint64
	GC                            uint32
	Goroutines                    int
	GCCPUFraction                 float64
	CPUSeconds                    float64
}

func qualificationRSS() uint64 {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.ParseUint(f[1], 10, 64)
	return n * uint64(os.Getpagesize())
}

// DIMSUM_DHCP_QUALIFY=1 enables paired, real UDP DNS measurements using the
// production resolver, transport, DHCP owner/store and name publication. DHCP
// packets/probes are injected; Linux L2 interoperability is qualified separately.
// Run one process at a time; timings are evidence, not flaky CI assertions.
func TestDHCPQualificationCoexistence(t *testing.T) {
	if os.Getenv("DIMSUM_DHCP_QUALIFY") != "1" {
		t.Skip("opt-in paired performance qualification")
	}
	mode := os.Getenv("DIMSUM_DHCP_MODE")
	full := os.Getenv("DIMSUM_DHCP_SERVICE_TEST") == "1"
	if full {
		require.Equal(t, "linux", runtime.GOOS, "full service fixture binds isolated namespace port 53")
	}
	if mode == "" {
		mode = "idle256"
	}
	require.Contains(t, []string{"idle", "idle256", "renewal", "reconnect128", "pagination", "slow", "failed", "settings", "random1024", "random4096"}, mode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	text := `version: 1
dns:
  listen: [0.0.0.0:53]
admin:
  listen: 127.0.0.1:0
paths:
  data_dir: data
  secrets_dir: secrets
dhcp:
  enabled: true
  interface: fixture0
  server_ip: 198.18.0.2
  gateway: 198.18.0.1
  subnet: 198.18.0.0/16
  range_start: 198.18.1.0
  range_end: 198.18.254.255
  lease_seconds: 3600
  local_domain: home.arpa
rules:
  - {id: qualification, action: deny, kind: exact, pattern: blocked.test, enabled: true}
`
	if mode == "random4096" {
		text = strings.Replace(text, "rules:", "  max_leases: 4096\nrules:", 1)
	}
	if full {
		text = strings.Replace(text, "  enabled: true", "  enabled: false", 1)
	}
	mixed := os.Getenv("DIMSUM_DNS_QUERY") == "mixed"
	if mixed {
		upstream, e := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
			var request transport.Request
			if dnswire.ParseRequest(r.Wire, &request.Message) != nil {
				return testutil.Response{Drop: true}
			}
			out := make([]byte, 1232)
			n, e := dnswire.BuildReply(out, &request.Message, dnswire.Reply{Null: true, TTL: 3600, RecursionAvailable: true}, 1232)
			if e != nil {
				return testutil.Response{Drop: true}
			}
			return testutil.Response{Wire: out[:n]}
		})
		require.NoError(t, e)
		defer upstream.Close()
		text = strings.Replace(text, "  listen: [0.0.0.0:53]", "  listen: [0.0.0.0:53]\n  upstreams: ["+upstream.Address()+"]", 1)
	}
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	configStore, err := config.OpenStore(ctx, path, path+".state", config.StoreOptions{Offline: true})
	require.NoError(t, err)
	var sup *DHCPSupervisor
	var pipeline *resolve.Pipeline
	var address *net.UDPAddr
	if full {
		service := new(Service)
		require.NoError(t, service.StartManaged(ctx, configStore))
		defer service.Close()
		sup, pipeline = service.dhcp, service.pipeline
		address = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 53}
	} else {
		sup = NewDHCPSupervisor(dir, []string{"0.0.0.0:53"})
		defer sup.Close(context.Background())
		publication := newDHCPNames(sup.View)
		pipeline = resolve.NewWithStore(nil, configStore)
		pipeline.SetLeases(publication.capture)
		defer pipeline.Close()
		server, e := transport.New(transport.Options{}, pipeline)
		require.NoError(t, e)
		socket, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		require.NoError(t, e)
		served := make(chan error, 1)
		go func() { served <- server.ServeUDP(ctx, socket) }()
		defer func() { cancel(); socket.Close(); <-served }()
		address = socket.LocalAddr().(*net.UDPAddr)
	}
	setEnabled := func(enabled bool) {
		t.Helper()
		api := control.New(control.Options{Store: configStore, ConfigPath: path})
		status, e := api.Status()
		require.NoError(t, e)
		_, e = api.DHCPMutate(ctx, "PATCH", "", false, control.DHCPMutation{Revision: status.SavedRevision, Edits: []config.Edit{{Path: []string{"enabled"}, Value: enabled}}})
		require.NoError(t, e)
	}
	client, err := net.DialUDP("udp4", nil, address)
	require.NoError(t, err)
	defer client.Close()
	query := []byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'b', 'l', 'o', 'c', 'k', 'e', 'd', 4, 't', 'e', 's', 't', 0, 0, 1, 0, 1}
	cacheQuery := []byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 6, 'c', 'a', 'c', 'h', 'e', 'd', 4, 't', 'e', 's', 't', 0, 0, 1, 0, 1}
	var reply [1232]byte
	samples := make([]int64, 0, 300000)
	qps, err := strconv.Atoi(os.Getenv("DIMSUM_DNS_QPS"))
	if os.Getenv("DIMSUM_DNS_QPS") != "" {
		require.NoError(t, err)
		require.GreaterOrEqual(t, qps, 0)
	}
	sampleDuration := 2 * time.Second
	if raw := os.Getenv("DIMSUM_DHCP_SAMPLE_SECONDS"); raw != "" {
		seconds, e := strconv.Atoi(raw)
		require.NoError(t, e)
		require.Greater(t, seconds, 0)
		sampleDuration = time.Duration(seconds) * time.Second
	}
	measure := func(label string) qualificationSample {
		samples = samples[:0]
		var before, after runtime.MemStats
		var cpuBefore, cpuAfter syscall.Rusage
		_ = syscall.Getrusage(syscall.RUSAGE_SELF, &cpuBefore)
		runtime.ReadMemStats(&before)
		start := time.Now()
		for time.Since(start) < sampleDuration {
			if qps > 0 {
				time.Sleep(time.Until(start.Add(time.Duration(len(samples)) * time.Second / time.Duration(qps))))
			}
			sent := time.Now()
			_ = client.SetDeadline(sent.Add(time.Second))
			wire := query
			if mixed && len(samples)%2 == 0 {
				wire = cacheQuery
			}
			_, err = client.Write(wire)
			if err != nil {
				break
			}
			var n int
			n, err = client.Read(reply[:])
			if err != nil {
				break
			}
			if n < 12 || reply[0] != query[0] || reply[1] != query[1] {
				err = errors.New("invalid DNS response")
				break
			}
			samples = append(samples, time.Since(sent).Nanoseconds())
		}
		elapsed := time.Since(start)
		_ = syscall.Getrusage(syscall.RUSAGE_SELF, &cpuAfter)
		require.NoError(t, err)
		require.NotEmpty(t, samples)
		runtime.GC()
		runtime.ReadMemStats(&after)
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		v := qualificationSample{Label: label, Queries: len(samples), QPS: float64(len(samples)) / elapsed.Seconds(), P50NS: samples[len(samples)/2], P95NS: samples[len(samples)*95/100], P99NS: samples[len(samples)*99/100], Heap: after.HeapAlloc, RSS: qualificationRSS(), Allocated: after.TotalAlloc - before.TotalAlloc, PauseNS: after.PauseTotalNs - before.PauseTotalNs, GC: after.NumGC - before.NumGC, Goroutines: runtime.NumGoroutine(), GCCPUFraction: after.GCCPUFraction}
		v.CPUSeconds = float64(cpuAfter.Utime.Sec+cpuAfter.Stime.Sec-cpuBefore.Utime.Sec-cpuBefore.Stime.Sec) + float64(cpuAfter.Utime.Usec+cpuAfter.Stime.Usec-cpuBefore.Utime.Usec-cpuBefore.Stime.Usec)/1e6
		if directory := os.Getenv("DIMSUM_DHCP_PROFILE_DIR"); directory != "" {
			file, e := os.Create(filepath.Join(directory, fmt.Sprintf("goroutines-%s-%s-%d.txt", mode, label, time.Now().UnixNano())))
			require.NoError(t, e)
			require.NoError(t, pprof.Lookup("goroutine").WriteTo(file, 1))
			require.NoError(t, file.Close())
		}
		b, e := json.Marshal(v)
		require.NoError(t, e)
		t.Log(string(b))
		return v
	}
	measure("warmup")
	before := measure("disabled-before")
	assert.Equal(t, "disabled", sup.Status().State)
	_, err = os.Stat(sup.dir)
	require.True(t, os.IsNotExist(err))
	count := 256
	if mode == "random1024" {
		count = 1024
	}
	if mode == "random4096" {
		count = 4096
	}
	if mode == "idle" || mode == "reconnect128" {
		count = 0
	}
	db, _, err := dhcp.OpenLeaseStore(sup.dir, dhcp.MaximumLeases, nil)
	require.NoError(t, err)
	leases := make([]dhcp.Lease, count)
	for i := range leases {
		mac := [6]byte{2, 0, 0, 0, byte(i >> 8), byte(i)}
		ip := netip.AddrFrom4([4]byte{198, 18, byte(1 + i/256), byte(i)})
		leases[i] = dhcp.Lease{Identity: "mac:" + string(mac[:]), MAC: mac, Address: ip, Hostname: fmt.Sprintf("client-%d", i), State: dhcp.Bound, Expiry: time.Now().Add(time.Hour), HoldUntil: time.Now().Add(2 * time.Hour)}
		require.NoError(t, db.Submit(ctx, dhcp.Mutation{Token: dhcp.Token{Generation: 1, Sequence: uint64(i + 1)}, Kind: dhcp.PutLease, Lease: leases[i]}))
		if (i+1)%16 == 0 || i+1 == len(leases) {
			batch := 16
			if (i+1)%16 != 0 {
				batch = (i + 1) % 16
			}
			for range batch {
				r := <-db.Results()
				require.NoError(t, r.Err)
			}
		}
	}
	require.NoError(t, db.Close(ctx))
	link := &qualificationLink{appDHCPPacketLink: appDHCPPacketLink{appDHCPLink: appDHCPLink{done: make(chan struct{})}, packets: make(chan []byte, 32)}, offers: make(chan dhcp.WireReply, 32)}
	sup.openLink = func(dhcp.Settings) (dhcp.Link, dhcp.ProbeFunc, error) {
		return link, func(ctx context.Context, _ netip.Addr) (bool, error) {
			select {
			case <-time.After(200 * time.Millisecond):
				return false, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}, nil
	}
	var failed *qualificationWriter
	var slow *stalledDHCPWriter
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	if mode == "slow" || mode == "failed" {
		sup.openStore = func(path string) (dhcp.LeaseWriter, dhcp.LeaseRecovery, error) {
			store, recovery, e := dhcp.OpenLeaseStore(path, 1024, nil)
			if mode == "failed" {
				failed = &qualificationWriter{LeaseWriter: store}
				return failed, recovery, e
			}
			slow = &stalledDHCPWriter{inner: store, entered: make(chan struct{}), release: release, results: make(chan dhcp.CommitResult, 1)}
			return slow, recovery, e
		}
	}
	if full {
		setEnabled(true)
	}
	settings := configStore.Snapshot().Config().DHCP
	require.NoError(t, sup.Reconcile(ctx, settings, configStore.Snapshot().Generation()))
	if failed != nil {
		failed.fail.Store(true)
	}
	workCtx, stopWork := context.WithCancel(ctx)
	defer stopWork()
	workDone := make(chan error, 1)
	var operations atomic.Int64
	startWork := time.Now()
	go func() {
		var workerErr error
		defer func() { workDone <- workerErr }()
		api := control.New(control.Options{Store: configStore, ConfigPath: path, DHCPInspect: sup.Inspect})
		if mode == "reconnect128" {
			retry := time.NewTicker(250 * time.Millisecond)
			defer retry.Stop()
			for {
				select {
				case <-workCtx.Done():
					return
				case offer := <-link.offers:
					o, e := dhcpv4.FromBytes(offer.Payload)
					if e != nil {
						workerErr = e
						return
					}
					r, e := dhcpv4.NewRequestFromOffer(o)
					if e != nil {
						workerErr = e
						return
					}
					select {
					case link.packets <- r.ToBytes():
					case <-workCtx.Done():
						return
					}
				case <-retry.C:
					bound := make(map[[6]byte]bool, 128)
					if view := sup.View(); view != nil {
						for j := 0; j < view.Len(); j++ {
							lease := view.Lease(j)
							if lease.State == dhcp.Bound {
								bound[lease.MAC] = true
							}
						}
					}
					if len(bound) == 128 {
						return
					}
					for j := 0; j < 128; j++ {
						mac := [6]byte{2, 0, 0, 0, 1, byte(j)}
						if bound[mac] {
							continue
						}
						p, e := dhcpv4.NewDiscovery(mac[:], dhcpv4.WithBroadcast(true))
						if e != nil {
							workerErr = e
							return
						}
						select {
						case link.packets <- p.ToBytes():
							operations.Add(1)
						case <-workCtx.Done():
							return
						}
					}
				}
			}
		}
		cursor := ""
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for i := 0; ; i++ {
			select {
			case <-workCtx.Done():
				return
			case <-tick.C:
			}
			switch mode {
			case "random1024", "random4096":
				// A deterministic permutation produces distinct synthetic identities.
				x := uint32(i+1) * 2654435761
				wire := make([]byte, 244)
				wire[0] = 1
				wire[1] = 1
				wire[2] = 6
				binary.BigEndian.PutUint32(wire[4:8], x)
				copy(wire[28:34], []byte{2, 1, byte(x >> 24), byte(x >> 16), byte(x >> 8), byte(x)})
				copy(wire[236:], []byte{99, 130, 83, 99, 53, 1, 1, 255})
				select {
				case link.packets <- wire:
					operations.Add(1)
				case <-workCtx.Done():
					return
				}
			case "pagination":
				page, e := api.DHCPLeases(url.Values{"limit": {"100"}, "cursor": {cursor}})
				if e != nil {
					workerErr = e
					return
				}
				cursor = page.NextCursor
				if _, e = json.Marshal(page); e != nil {
					workerErr = e
					return
				}
				operations.Add(1)
			case "renewal", "slow", "failed":
				lease := leases[i%len(leases)]
				if mode == "slow" {
					// One admitted writer is held; duplicate renewals must coalesce
					// without accumulating workers behind that operation.
					lease = leases[0]
				}
				wire := make([]byte, 244)
				wire[0] = 1
				wire[1] = 1
				wire[2] = 6
				binary.BigEndian.PutUint32(wire[4:8], uint32(i+1))
				copy(wire[12:16], lease.Address.AsSlice())
				copy(wire[28:34], lease.MAC[:])
				copy(wire[236:], []byte{99, 130, 83, 99, 53, 1, 3, 255})
				select {
				case link.packets <- wire:
					operations.Add(1)
				case <-workCtx.Done():
					return
				}
			case "settings":
				if i%100 != 0 {
					continue
				}
				status, e := api.Status()
				if e != nil {
					workerErr = e
					return
				}
				_, e = api.DHCPMutate(workCtx, "PATCH", "", false, control.DHCPMutation{Revision: status.SavedRevision, Edits: []config.Edit{{Path: []string{"lease_seconds"}, Value: 3600 + (i/100)%2}}})
				if e != nil {
					if workCtx.Err() == nil {
						workerErr = e
					}
					return
				}
				snap := configStore.Snapshot()
				if e := sup.Reconcile(workCtx, snap.Config().DHCP, snap.Generation()); e != nil {
					if workCtx.Err() == nil {
						workerErr = e
					}
					return
				}
				operations.Add(1)
			}
		}
	}()
	if slow != nil {
		select {
		case <-slow.entered:
		case <-time.After(time.Second):
			t.Fatal("slow writer not entered")
		}
	}
	for i := 0; i < 3 || mode == "reconnect128" && (sup.View() == nil || sup.View().Len() < 128) && time.Since(startWork) < 60*time.Second; i++ {
		current := measure(fmt.Sprintf("%s-%d", mode, i))
		t.Logf("delta heap=%d rss=%d vs disabled-before", int64(current.Heap)-int64(before.Heap), int64(current.RSS)-int64(before.RSS))
	}
	if mode == "reconnect128" {
		require.Eventually(t, func() bool { return len(sup.Leases()) == 128 && sup.View() != nil && sup.View().Len() == 128 }, 60*time.Second-time.Since(startWork), 20*time.Millisecond)
	}
	stopWork()
	if slow != nil {
		unblock()
	}
	require.NoError(t, <-workDone)
	if strings.HasPrefix(mode, "random") {
		assert.Len(t, sup.Leases(), count)
		assert.Zero(t, link.acks.Load())
	}
	t.Logf("workload=%s operations=%d acks=%d elapsed=%s status=%+v", mode, operations.Load(), link.acks.Load(), time.Since(startWork), sup.Status())
	if full {
		setEnabled(false)
		snap := configStore.Snapshot()
		require.NoError(t, sup.Reconcile(ctx, snap.Config().DHCP, snap.Generation()))
	} else {
		require.NoError(t, sup.Reconcile(ctx, dhcp.Settings{}, 10000))
	}
	measure("disabled-after")
	if mixed {
		t.Logf("mixed block/cache resolver counters: %+v", pipeline.CacheStats())
	}
}
