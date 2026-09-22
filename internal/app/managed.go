package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	apispec "github.com/richkeenan/dimsum/api"
	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/control"
	"github.com/richkeenan/dimsum/internal/dhcp"
	"github.com/richkeenan/dimsum/internal/mcpserver"
	"github.com/richkeenan/dimsum/internal/webassets"
)

type managedRuntime struct {
	store        *config.Store
	observations *observability
	admin        *admin.Server
	control      *control.Service
	local        net.Listener
	socket       string
	handler      http.Handler
	backupMu     sync.Mutex
	refreshMu    sync.Mutex
	backupID     string
	backup       []byte
	dhcp         *DHCPSupervisor
}

func newManagedRuntime(service *Service, store *config.Store, o *observability, address string) (*managedRuntime, error) {
	c := store.Snapshot().Config()
	if _, err := store.ActiveSecret(config.AdminSecretName); err != nil {
		if !os.IsNotExist(err) || c.Admin.SecretGeneration != "" {
			return nil, err
		}
		hash, err := admin.HashPassword("admin")
		if err != nil {
			return nil, err
		}
		if err = store.EnsureAdminSecret([]byte(hash + "\n")); err != nil {
			return nil, err
		}
	}
	m := &managedRuntime{store: store, observations: o}
	tokens, err := admin.OpenTokenStore(filepath.Join(store.ResolvePath(c.Paths.SecretsDir), "api-tokens.json"))
	if err != nil {
		return nil, fmt.Errorf("agent tokens: %w", err)
	}
	jobs := map[string]func(context.Context, json.RawMessage) (any, error){
		"dhcp-check":     dhcpDiagnostic(service, func() (dhcp.Settings, uint64) { snap := store.Snapshot(); return snap.Config().DHCP, snap.Generation() }),
		"upstream-probe": m.upstreamProbe,
		"support-bundle": m.supportBundle,
		"backup": func(ctx context.Context, _ json.RawMessage) (any, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			archive, err := store.Backup()
			if err != nil {
				return nil, err
			}
			var random [16]byte
			if _, err = rand.Read(random[:]); err != nil {
				return nil, err
			}
			id := hex.EncodeToString(random[:])
			m.backupMu.Lock()
			m.backupID = id
			m.backup = archive
			m.backupMu.Unlock()
			return map[string]any{"download_url": "/api/v1/config/backups/" + id, "format": "dimsum-config", "contains_secrets": true, "expires": "next_backup_or_restart"}, nil
		},
		"restore": func(ctx context.Context, input json.RawMessage) (any, error) {
			var request struct {
				Revision string `json:"revision"`
				Archive  string `json:"archive"`
			}
			if err := json.Unmarshal(input, &request); err != nil {
				return nil, err
			}
			if request.Revision == "" {
				return nil, fmt.Errorf("restore requires saved revision")
			}
			archive, err := base64.StdEncoding.DecodeString(request.Archive)
			if err != nil {
				return nil, err
			}
			result, err := store.Restore(ctx, request.Revision, archive)
			m.refresh()
			return safeJSON(result), err
		},
	}
	m.control = control.New(control.Options{BootID: o.boot, DHCPStatus: func() any { return safeJSON(service.DHCPStatus()) }, DHCPInspect: service.DHCPInspect, Store: store, ConfigPath: store.ConfigPath(), Provider: NewHistoryProvider(o.db, service.ClientName), Jobs: jobs, Diagnostics: func(context.Context) (any, error) {
		transport, cache := service.DNSStats()
		interfaces := dnsInterfaces()
		return map[string]any{"dhcp": safeJSON(service.DHCPStatus()), "naming": safeJSON(service.NamingDiagnostics()), "dns_ready": service.Ready(), "dns_addresses": clientDNSAddresses(service.Addresses().DNS, interfaces), "boot_id": o.boot, "process": safeJSON(o.collector.Snapshot()), "transport": safeJSON(transport), "cache": safeJSON(cache), "storage": safeJSON(o.status()), "upstreams": safeJSON(service.UpstreamHealth())}, nil
	}})
	// Configured hostnames are read from the active snapshot on each request.
	allowed := []string{address}
	host, port, _ := net.SplitHostPort(address)
	if host == "0.0.0.0" || host == "::" {
		if interfaces, err := net.InterfaceAddrs(); err == nil {
			for _, iface := range interfaces {
				ip, _, err := net.ParseCIDR(iface.String())
				if err == nil {
					allowed = append(allowed, net.JoinHostPort(ip.String(), port))
				}
			}
		}
	}
	if host == "127.0.0.1" || host == "::1" || host == "0.0.0.0" || host == "::" {
		allowed = append(allowed, net.JoinHostPort("localhost", port))
	}
	openAPI, err := apispec.JSON()
	if err != nil {
		m.control.Close()
		return nil, fmt.Errorf("OpenAPI: %w", err)
	}
	mcp, err := mcpserver.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.admin.LocalHandler().ServeHTTP(w, r)
	}))
	if err != nil {
		m.control.Close()
		return nil, fmt.Errorf("MCP: %w", err)
	}
	m.admin = admin.New(m.control, admin.Options{Tokens: tokens, MCP: mcp, OpenAPIJSON: openAPI, OpenAPIYAML: apispec.YAML, AllowedHosts: allowed, SecureCookies: c.Admin.SecureCookies, DownloadBackup: func(ctx context.Context, id string) (io.ReadCloser, int64, error) {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		m.backupMu.Lock()
		defer m.backupMu.Unlock()
		if id == "" || id != m.backupID {
			return nil, 0, os.ErrNotExist
		}
		return io.NopCloser(bytes.NewReader(m.backup)), int64(len(m.backup)), nil
	}})
	m.refresh()
	m.socket = c.Admin.ControlSocket
	if m.socket != "" {
		m.socket = store.ResolvePath(m.socket)
	} else {
		m.socket = filepath.Join(store.ResolvePath(c.Paths.DataDir), "control.sock")
		// macOS Unix socket names have a short limit; long test/config paths
		// use an owner-private, deterministic runtime directory instead.
		if len(m.socket) >= 100 {
			absolute, _ := filepath.Abs(store.ConfigPath())
			sum := sha256.Sum256([]byte(absolute))
			m.socket = filepath.Join(os.TempDir(), "dimsum-"+strconv.Itoa(os.Getuid())+"-"+hex.EncodeToString(sum[:6]), "c.sock")
		}
	}
	if err := os.MkdirAll(filepath.Dir(m.socket), 0700); err != nil {
		m.control.Close()
		return nil, err
	}
	m.local, err = m.admin.ListenUnix(m.socket)
	if err != nil {
		m.control.Close()
		return nil, err
	}
	api := m.admin.Handler()
	assets := webassets.Handler()
	m.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/mcp" || r.URL.Path == "/session" || strings.HasPrefix(r.URL.Path, "/health/") {
			api.ServeHTTP(w, r)
			return
		}
		assets.ServeHTTP(w, r)
	})
	return m, nil
}

func (m *managedRuntime) refresh() {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	m.admin.RefreshPasswordHash()
	m.observations.retention(m.store.Snapshot().Config().Statistics)
}
func (m *managedRuntime) watch(ctx context.Context) {
	reconcile := func() {
		if m.dhcp != nil {
			snapshot := m.store.Snapshot()
			applyCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			_ = m.dhcp.Reconcile(applyCtx, snapshot.Config().DHCP, snapshot.Generation())
			cancel()
		}
	}
	reconcile()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.refresh()
			reconcile()
		}
	}
}

// Diagnostic counters can exceed JavaScript's exact integer range. These cold
// responses encode all numeric values as decimal strings, never rounded floats.
func safeJSON(value any) any {
	b, err := json.Marshal(value)
	if err != nil {
		return map[string]any{"error": "diagnostic encoding failed"}
	}
	var out any
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	if d.Decode(&out) != nil {
		return nil
	}
	var convert func(any) any
	convert = func(v any) any {
		switch x := v.(type) {
		case json.Number:
			return x.String()
		case map[string]any:
			for k, v := range x {
				x[k] = convert(v)
			}
		case []any:
			for i, v := range x {
				x[i] = convert(v)
			}
		}
		return v
	}
	return convert(out)
}
