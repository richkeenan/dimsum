package webassets_test

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/app"
	"github.com/richkeenan/dimsum/internal/cli"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrowserAgainstManagedRuntime(t *testing.T) {
	if os.Getenv("DIMSUM_BROWSER_TEST") != "1" {
		t.Skip("set DIMSUM_BROWSER_TEST=1 with built frontend and installed Chromium")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "dimsum.yaml")
	source := `# Isolated managed browser fixture; no production listeners.
version: 1
dns:
  listen: [127.0.0.1:0]
  upstreams: [127.0.0.1:9]
admin:
  listen: 127.0.0.1:0
paths:
  data_dir: data
  secrets_dir: secrets
cache:
  max_stale_seconds: 3600
records:
  - name: printer.home.arpa
    type: A
    value: 192.0.2.20
    ttl: 60
rules:
  - id: browser-block
    kind: exact
    action: deny
    pattern: ads.example.test
    enabled: true
clients:
  - address: 127.0.0.1
    name: Browser fixture
`
	require.NoError(t, os.WriteFile(path, []byte(source), 0600))
	passwordPath := filepath.Join(dir, "password")
	password := "isolated-managed-browser-password"
	require.NoError(t, os.WriteFile(passwordPath, []byte(password), 0600))
	bootstrap := exec.CommandContext(t.Context(), "go", "run", "../../cmd/dimsum", "bootstrap", "-config", path, "-password-file", passwordPath)
	output, err := bootstrap.CombinedOutput()
	require.NoError(t, err, string(output))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store, err := config.OpenStore(ctx, path, path+".state", config.StoreOptions{Offline: true, PollInterval: 20 * time.Millisecond})
	require.NoError(t, err)
	service := new(app.Service)
	require.NoError(t, service.StartManaged(ctx, store))
	t.Cleanup(func() { assert.NoError(t, service.Close()) })
	client := dns.Client{Timeout: time.Second}
	for i := 0; i < 122; i++ {
		name := "printer.home.arpa."
		if i >= 110 {
			name = "ads.example.test."
		}
		q := new(dns.Msg)
		q.SetQuestion(name, dns.TypeA)
		answer, _, err := client.Exchange(q, service.Addresses().DNS[0])
		require.NoError(t, err)
		require.NotNil(t, answer)
	}
	window := url.Values{"limit": {"200"}, "from": {time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}, "to": {time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}}.Encode()
	require.Eventually(t, func() bool {
		var out, stderr bytes.Buffer
		code := cli.Run(ctx, []string{"--socket", service.Addresses().Control, "queries", "--query", window}, &out, &stderr)
		return code == 0 && bytes.Contains(out.Bytes(), []byte("printer.home.arpa"))
	}, 5*time.Second, 20*time.Millisecond)
	command := exec.CommandContext(ctx, "npm", "run", "test:e2e", "--", "managed-api.spec.ts", "--workers=1")
	command.Dir = "../../web"
	command.Env = append(os.Environ(), "DIMSUM_E2E_URL=http://"+service.Addresses().Admin, "DIMSUM_E2E_PASSWORD="+password, "DIMSUM_E2E_MANAGED_CONFIG="+path)
	output, err = command.CombinedOutput()
	require.NoError(t, err, string(output))
	t.Log(string(output))
}
