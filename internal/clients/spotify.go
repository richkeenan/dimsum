package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The endpoint is internal evidence from a live, unambiguous DNS-SD graph.
// HTTP always targets the observed literal address, never a supplied hostname.
type spotifyEndpoint struct {
	Address              netip.Addr
	Interface            int
	Host, Instance, Path string
	Port                 uint16
}

func validSpotifyPath(path string) bool {
	if len(path) == 0 || len(path) > 256 || path[0] != '/' || strings.HasPrefix(path, "//") {
		return false
	}
	for _, r := range path {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/-_.~", r)) {
			return false
		}
	}
	return true
}

func newSpotifyClient() *http.Client {
	return &http.Client{
		Timeout:       2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 8192, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext},
	}
}

func fetchSpotify(ctx context.Context, client *http.Client, endpoint spotifyEndpoint) (Evidence, error) {
	if !endpoint.Address.IsValid() || endpoint.Port == 0 || !validSpotifyPath(endpoint.Path) {
		return Evidence{}, fmt.Errorf("invalid Spotify endpoint")
	}
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(endpoint.Address.String(), strconv.Itoa(int(endpoint.Port))), Path: endpoint.Path, RawQuery: "action=getInfo"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Evidence{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Evidence{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Evidence{}, fmt.Errorf("Spotify HTTP status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil {
		return Evidence{}, err
	}
	if len(body) > 65536 {
		return Evidence{}, fmt.Errorf("Spotify response too large")
	}
	// Whitelist identity fields; account IDs, keys and credentials are discarded.
	var info struct {
		Status int    `json:"status"`
		Name   string `json:"remoteName"`
		Type   string `json:"deviceType"`
		Brand  string `json:"brandDisplayName"`
		Model  string `json:"modelDisplayName"`
	}
	if err = json.Unmarshal(body, &info); err != nil {
		return Evidence{}, err
	}
	if info.Status != 101 {
		return Evidence{}, fmt.Errorf("Spotify discovery status %d", info.Status)
	}
	brand, model := deviceProduct(info.Brand, info.Model)
	label := safeLabel(info.Name)
	kind := ""
	switch strings.ToUpper(info.Type) {
	case "SPEAKER":
		kind = "speaker"
	case "GAMECONSOLE":
		kind = "console"
	case "TV":
		kind = "tv"
	case "SMARTPHONE":
		kind = "phone"
	case "TABLET":
		kind = "tablet"
	}
	if label == "" && brand == "" && model == "" && kind == "" {
		return Evidence{}, fmt.Errorf("no Spotify identity fields")
	}
	return Evidence{Source: "spotify-connect", ServiceType: "_spotify-connect._tcp", Hostname: endpoint.Host, Label: label, Manufacturer: brand, Model: model, DeviceType: kind}, nil
}

type spotifyState struct {
	evidence   Evidence
	next, seen time.Time
}
type spotifyResult struct {
	endpoint spotifyEndpoint
	evidence Evidence
	err      error
}
type spotifyDiscovery struct {
	states  map[spotifyEndpoint]spotifyState
	jobs    chan spotifyEndpoint
	results chan spotifyResult
	cancel  context.CancelFunc
	done    chan struct{}
}

func newSpotifyDiscovery(ctx context.Context, fetch func(context.Context, spotifyEndpoint) (Evidence, error)) *spotifyDiscovery {
	ctx, cancel := context.WithCancel(ctx)
	s := &spotifyDiscovery{states: map[spotifyEndpoint]spotifyState{}, jobs: make(chan spotifyEndpoint), results: make(chan spotifyResult, 1), cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		for {
			select {
			case <-ctx.Done():
				return
			case endpoint := <-s.jobs:
				e, err := fetch(ctx, endpoint)
				select {
				case s.results <- spotifyResult{endpoint, e, err}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return s
}

func (s *spotifyDiscovery) close() { s.cancel(); <-s.done }

// Called only by the mDNS loop. No locks or HTTP operations on the read path.
func (s *spotifyDiscovery) apply(n Name, now time.Time) Name {
	for {
		select {
		case result := <-s.results:
			state, ok := s.states[result.endpoint]
			if !ok {
				continue
			}
			state.next = now.Add(time.Minute)
			if result.err == nil {
				state.evidence = result.evidence
				state.evidence.Updated = now
				state.evidence.Expires = now.Add(5 * time.Minute)
				state.next = state.evidence.Expires
			}
			s.states[result.endpoint] = state
		default:
			goto drained
		}
	}
drained:
	for key, state := range s.states {
		if now.Sub(state.seen) > 10*time.Minute {
			delete(s.states, key)
		}
	}
	if n.Device == nil {
		return n
	}
	n.Device = cloneDevice(n.Device)
	for i, e := range n.Device.Evidence {
		key := e.spotify
		if !key.Address.IsPrivate() || key.Port == 0 || !validSpotifyPath(key.Path) || !now.Before(e.Expires) {
			continue
		}
		state, exists := s.states[key]
		if !exists && len(s.states) >= 128 {
			continue
		}
		state.seen = now
		if !now.Before(state.next) {
			select {
			case s.jobs <- key:
				state.next = now.Add(time.Minute)
			default:
			}
		}
		s.states[key] = state
		if now.Before(state.evidence.Expires) {
			extra := state.evidence
			extra.Expires = minTime(extra.Expires, e.Expires)
			if len(n.Device.Evidence) < 16 {
				n.Device.Evidence = append(n.Device.Evidence, extra)
			} else {
				// At the presentation cap, replace this endpoint's generic
				// advertisement with its richer identity, retaining its host/TTL.
				n.Device.Evidence[i] = extra
			}
		}
	}
	return n
}
