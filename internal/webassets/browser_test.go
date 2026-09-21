package webassets_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	apispec "github.com/richkeenan/dimsum/api"
	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/control"
	"github.com/richkeenan/dimsum/internal/mcpserver"
	"github.com/richkeenan/dimsum/internal/webassets"
	"github.com/stretchr/testify/require"
)

// Opt-in because ordinary Go builds/tests deliberately do not require Node.
// This exercises the real authenticated HTTP adapter and coordinator on an
// isolated ephemeral listener. No DNS listener or production files are opened.
func TestBrowserAgainstGoAPI(t *testing.T) {
	if os.Getenv("DIMSUM_BROWSER_TEST") != "1" {
		t.Skip("set DIMSUM_BROWSER_TEST=1 after installing web dependencies and Chromium")
	}
	dir := t.TempDir()
	source, err := os.ReadFile("../../testdata/config/dimsum.yaml")
	require.NoError(t, err)
	configPath := filepath.Join(dir, "dimsum.yaml")
	require.NoError(t, os.WriteFile(configPath, source, 0600))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store, err := config.OpenStore(ctx, configPath, filepath.Join(dir, "state"), config.StoreOptions{Offline: true, PollInterval: 20 * time.Millisecond})
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { defer close(done); store.Watch(ctx) }()
	defer func() { cancel(); <-done }()
	service := control.New(control.Options{Store: store, ConfigPath: configPath})
	password := "isolated-browser-test-password"
	hash, err := admin.HashPassword(password)
	require.NoError(t, err)
	server := httptest.NewUnstartedServer(nil)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "secrets"), 0700))
	tokens, err := admin.OpenTokenStore(filepath.Join(dir, "secrets", "api-tokens.json"))
	require.NoError(t, err)
	openAPI, err := apispec.JSON()
	require.NoError(t, err)
	var adapter *admin.Server
	mcp, err := mcpserver.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		adapter.LocalHandler().ServeHTTP(w, r)
	}))
	require.NoError(t, err)
	adapter = admin.New(service, admin.Options{Tokens: tokens, MCP: mcp, OpenAPIJSON: openAPI, OpenAPIYAML: apispec.YAML, PasswordHash: hash, AllowedHosts: []string{server.Listener.Addr().String()}})
	mux := http.NewServeMux()
	mux.Handle("/api/", adapter.Handler())
	mux.Handle("/mcp", adapter.Handler())
	mux.Handle("/session", adapter.Handler())
	mux.Handle("/health/", adapter.Handler())
	mux.Handle("/", webassets.Handler())
	server.Config.Handler = mux
	server.Start()
	defer server.Close()
	command := exec.CommandContext(ctx, "npm", "run", "test:e2e", "--", "real-api.spec.ts", "--workers=1")
	command.Dir = "../../web"
	command.Env = append(os.Environ(), "DIMSUM_E2E_URL="+server.URL, "DIMSUM_E2E_PASSWORD="+password, "DIMSUM_E2E_CONFIG="+configPath)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	t.Log(string(output))
}
