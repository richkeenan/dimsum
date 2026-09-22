package clients

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpotifyInformation(t *testing.T) {
	for _, tc := range []struct{ body, name, brand, model, kind string }{
		{`{"status":101,"remoteName":"","deviceType":"SPEAKER","brandDisplayName":"Amazon","modelDisplayName":"Echo_Dot","activeUser":"not-retained"}`, "Amazon Echo Dot", "Amazon", "Echo Dot", "speaker"},
		{`{"status":101,"remoteName":"PS5-Example","deviceType":"GAMECONSOLE","brandDisplayName":"sony_tv","modelDisplayName":"ps5"}`, "PS5-Example", "Sony", "PlayStation 5", "console"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			requests := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r.Method + " " + r.URL.RequestURI()
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			client := newSpotifyClient()
			client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			endpoint := spotifyEndpoint{Address: netip.MustParseAddr("192.0.2.20"), Port: 4070, Path: "/spotifyConnect", Host: "none.local"}
			e, err := fetchSpotify(context.Background(), client, endpoint)
			require.NoError(t, err)
			assert.Equal(t, "GET /spotifyConnect?action=getInfo", <-requests)
			assert.Equal(t, tc.brand, e.Manufacturer)
			assert.Equal(t, tc.model, e.Model)
			assert.Equal(t, tc.kind, e.DeviceType)
			now := time.Now()
			e.Expires = now.Add(time.Minute)
			n := enrichDiscovered(Name{Name: "none.local", Expires: e.Expires, Device: &Enrichment{Hostname: "none.local", Evidence: []Evidence{e}}}, now)
			assert.Equal(t, tc.name, n.Name)
			assert.Equal(t, tc.kind, n.Device.Category)
			assert.Equal(t, "spotify-connect", n.Source)
		})
	}
}

func TestSpotifyRejectsBadResponsesAndRedirects(t *testing.T) {
	var redirected atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer sink.Close()
	for _, body := range []string{`{"status":102}`, `{broken`, strings.Repeat(" ", 65537), `{"status":101} trailing`, `{"status":101,"remoteName":"bad\nname"}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		client := newSpotifyClient()
		client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		}
		_, err := fetchSpotify(context.Background(), client, spotifyEndpoint{Address: netip.MustParseAddr("192.0.2.20"), Port: 1234, Path: "/info"})
		assert.Error(t, err)
		server.Close()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, sink.URL, http.StatusFound) }))
	defer server.Close()
	client := newSpotifyClient()
	client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	_, err := fetchSpotify(context.Background(), client, spotifyEndpoint{Address: netip.MustParseAddr("192.0.2.20"), Port: 1234, Path: "/info"})
	assert.Error(t, err)
	assert.Zero(t, redirected.Load())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = fetchSpotify(ctx, client, spotifyEndpoint{Address: netip.MustParseAddr("192.0.2.20"), Port: 1234, Path: "/info"})
	assert.Error(t, err)
	for _, path := range []string{"", "//example.com/info", "http://example.com", "/info?action=addUser", "/info#fragment", "/bad\npath", "/%2f%2fexample.com"} {
		assert.False(t, validSpotifyPath(path), path)
	}
	assert.True(t, validSpotifyPath("/spotifyConnect"))
}

func TestSpotifyGraphRequiresUnambiguousLiveEndpoint(t *testing.T) {
	now := time.Now()
	c := newMDNSCache()
	require.NoError(t, c.ingest(2, mdnsPacket(t,
		rr(t, "_spotify-connect._tcp.local. 120 IN PTR SpotifyConnect._spotify-connect._tcp.local."),
		rr(t, "SpotifyConnect._spotify-connect._tcp.local. 120 IN SRV 0 0 4070 none.local."),
		rr(t, "SpotifyConnect._spotify-connect._tcp.local. 1 IN TXT \"CPath=/spotifyConnect\""),
		rr(t, "none.local. 120 IN A 192.0.2.20")), now))
	n := c.lookup(netip.MustParseAddr("192.0.2.20"), now)
	require.NotNil(t, n.Device)
	var endpoint spotifyEndpoint
	for _, e := range n.Device.Evidence {
		if e.spotify.Port != 0 {
			endpoint = e.spotify
		}
	}
	assert.Equal(t, uint16(4070), endpoint.Port)
	assert.Equal(t, "/spotifyConnect", endpoint.Path)
	assert.Equal(t, n.Address, endpoint.Address)
	assert.Equal(t, 2, endpoint.Interface)
	after := c.lookup(n.Address, now.Add(2*time.Second))
	for _, e := range after.Device.Evidence {
		assert.Zero(t, e.spotify.Port)
	}
	// Competing SRV records must not choose an HTTP endpoint arbitrarily.
	require.NoError(t, c.ingest(2, mdnsPacket(t, rr(t, "SpotifyConnect._spotify-connect._tcp.local. 120 IN SRV 0 0 4071 none.local.")), now))
	for _, e := range c.lookup(n.Address, now).Device.Evidence {
		assert.Zero(t, e.spotify.Port)
	}
}

func TestSpotifyWorkerCachesAndExpiresEvidence(t *testing.T) {
	now := time.Now()
	// A synthetic private address is necessary to exercise the LAN-only scheduler.
	key := spotifyEndpoint{Address: netip.MustParseAddr("10.0.0.20"), Host: "none.local", Instance: "test", Port: 4070, Path: "/info"}
	var calls atomic.Int32
	s := newSpotifyDiscovery(context.Background(), func(ctx context.Context, key spotifyEndpoint) (Evidence, error) {
		calls.Add(1)
		return Evidence{Source: "spotify-connect", Hostname: key.Host, Model: "Echo Dot", Manufacturer: "Amazon", DeviceType: "speaker"}, nil
	})
	defer s.close()
	n := Name{Address: key.Address, Name: key.Host, Expires: now.Add(time.Hour), Device: &Enrichment{Hostname: key.Host, Evidence: []Evidence{{Source: "dns-sd", Hostname: key.Host, ServiceType: "_spotify-connect._tcp", Label: "SpotifyConnect", Expires: now.Add(time.Minute), spotify: key}}}}
	var got Name
	require.Eventually(t, func() bool { got = enrichDiscovered(s.apply(n, now), now); return got.Name == "Amazon Echo Dot" }, time.Second, time.Millisecond)
	assert.Equal(t, int32(1), calls.Load())
	assert.Equal(t, "speaker", got.Device.Category)
	assert.Equal(t, "none.local", got.Device.Hostname)
	assert.Equal(t, "Owner name", mergeDiscovered(Name{Name: "Owner name", Source: "override", Fresh: true}, got, now).Name)
	s.apply(n, now.Add(30*time.Second))
	assert.Equal(t, int32(1), calls.Load())
	assert.Empty(t, enrichDiscovered(got, now.Add(61*time.Second)).Device.Evidence)
	changed := n
	changed.Device = cloneDevice(n.Device)
	changed.Device.Evidence[0].spotify.Path = "/changed"
	// A response for the old endpoint must not enrich the replacement.
	first := s.apply(changed, now)
	assert.Len(t, first.Device.Evidence, 1)
}

func TestSpotifyWorkerCancelsInFlightLookup(t *testing.T) {
	started := make(chan struct{})
	s := newSpotifyDiscovery(context.Background(), func(ctx context.Context, key spotifyEndpoint) (Evidence, error) {
		close(started)
		<-ctx.Done()
		return Evidence{}, ctx.Err()
	})
	select {
	case s.jobs <- spotifyEndpoint{}:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	<-started
	s.close()
	select {
	case <-s.done:
	default:
		t.Fatal("worker still running")
	}
}

func TestSpotifyWorkerFailureCooldownAndLANBoundary(t *testing.T) {
	now := time.Now()
	var calls atomic.Int32
	s := newSpotifyDiscovery(context.Background(), func(context.Context, spotifyEndpoint) (Evidence, error) {
		calls.Add(1)
		return Evidence{}, fmt.Errorf("unavailable")
	})
	defer s.close()
	n := Name{Device: &Enrichment{Evidence: []Evidence{{Expires: now.Add(time.Hour), spotify: spotifyEndpoint{Address: netip.MustParseAddr("192.0.2.20"), Port: 4070, Path: "/info"}}}}}
	s.apply(n, now)
	assert.Empty(t, s.states, "public/documentation addresses do not trigger local HTTP")
	assert.Zero(t, calls.Load())
	n.Device.Evidence[0].spotify.Address = netip.MustParseAddr("10.0.0.20")
	require.Eventually(t, func() bool { s.apply(n, now); return !s.states[n.Device.Evidence[0].spotify].next.IsZero() }, time.Second, time.Millisecond)
	require.Eventually(t, func() bool { return len(s.results) == 1 }, time.Second, time.Millisecond)
	s.apply(n, now)
	for range 10 {
		s.apply(n, now.Add(30*time.Second))
	}
	assert.Equal(t, int32(1), calls.Load())
	require.Eventually(t, func() bool { s.apply(n, now.Add(61*time.Second)); return calls.Load() == 2 }, time.Second, time.Millisecond)
}

func TestSpotifyIdentitySurvivesFullEvidenceList(t *testing.T) {
	now := time.Now()
	key := spotifyEndpoint{Address: netip.MustParseAddr("10.0.0.20"), Host: "none.local", Port: 4070, Path: "/info"}
	s := newSpotifyDiscovery(context.Background(), func(context.Context, spotifyEndpoint) (Evidence, error) {
		return Evidence{}, fmt.Errorf("not expected")
	})
	defer s.close()
	s.states[key] = spotifyState{next: now.Add(time.Minute), seen: now, evidence: Evidence{Source: "spotify-connect", Hostname: key.Host, Manufacturer: "Amazon", Model: "Echo", DeviceType: "speaker", Expires: now.Add(time.Minute)}}
	n := Name{Name: key.Host, Expires: now.Add(time.Minute), Device: &Enrichment{Hostname: key.Host}}
	for range 16 {
		n.Device.Evidence = append(n.Device.Evidence, Evidence{Source: "dns-sd", Hostname: key.Host, Label: "SpotifyConnect", Expires: n.Expires})
	}
	n.Device.Evidence[0].spotify = key
	got := enrichDiscovered(s.apply(n, now), now)
	assert.Equal(t, "Amazon Echo", got.Name)
	assert.Equal(t, "speaker", got.Device.Category)
	assert.Len(t, got.Device.Evidence, 16)
}

func TestSpotifyHTTPTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	client := newSpotifyClient()
	client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	start := time.Now()
	_, err := fetchSpotify(context.Background(), client, spotifyEndpoint{Address: netip.MustParseAddr("192.0.2.20"), Port: 1234, Path: "/info"})
	require.Error(t, err)
	assert.Less(t, time.Since(start), 4*time.Second)
}

func TestSpotifyCacheCapacity(t *testing.T) {
	now := time.Now()
	s := newSpotifyDiscovery(context.Background(), func(ctx context.Context, _ spotifyEndpoint) (Evidence, error) {
		<-ctx.Done()
		return Evidence{}, ctx.Err()
	})
	defer s.close()
	for i := 0; i < 256; i++ {
		key := spotifyEndpoint{Address: netip.MustParseAddr("10.0.0.20"), Port: uint16(1024 + i), Path: "/info"}
		s.apply(Name{Device: &Enrichment{Evidence: []Evidence{{Expires: now.Add(time.Hour), spotify: key}}}}, now)
	}
	assert.Len(t, s.states, 128)
	s.apply(Name{}, now.Add(11*time.Minute))
	assert.Empty(t, s.states)
}

func TestMDNSViewChangeCancelsSpotifyAndDiscardsOldIdentity(t *testing.T) {
	v, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	var view atomic.Pointer[View]
	view.Store(v)
	m := New(view.Load)
	f := &fakeMDNS{packets: make(chan mdnsDatagram, 1), sent: make(chan []byte, 16)}
	m.openMDNS = func(context.Context, MDNSSettings) (mdnsTransport, []string) { return f, nil }
	started, canceled := make(chan struct{}), make(chan struct{})
	m.lookupSpotify = func(ctx context.Context, key spotifyEndpoint) (Evidence, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return Evidence{Source: "spotify-connect", Hostname: key.Host, Label: "Old identity"}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t,
		rr(t, "_spotify-connect._tcp.local. 120 IN PTR SpotifyConnect._spotify-connect._tcp.local."),
		rr(t, "SpotifyConnect._spotify-connect._tcp.local. 120 IN SRV 0 0 4070 none.local."),
		rr(t, "SpotifyConnect._spotify-connect._tcp.local. 120 IN TXT \"CPath=/info\""),
		rr(t, "none.local. 120 IN AAAA fd00::20"))}
	ip := netip.MustParseAddr("fd00::20")
	m.Observe(ip)
	select {
	case <-started:
	case <-time.After(4 * time.Second):
		t.Fatal("Spotify lookup did not start")
	}
	disabled, err := NewView(Settings{}, nil, nil)
	require.NoError(t, err)
	view.Store(disabled)
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("configuration change did not cancel lookup")
	}
	require.Eventually(t, func() bool { return !m.Diagnostics().Enabled }, time.Second, time.Millisecond)
	assert.Empty(t, m.Get(ip).Name)
	m.mu.Lock()
	assert.Empty(t, m.mdnsNames)
	m.mu.Unlock()
}
