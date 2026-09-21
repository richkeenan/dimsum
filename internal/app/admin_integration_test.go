package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/app"
	"github.com/richkeenan/dimsum/internal/cli"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedAdminHealthAndProtectedAPI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dimsum.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [127.0.0.1:9]\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: data\n  secrets_dir: secrets\n"), 0600))
	store, err := config.OpenStore(context.Background(), path, path+".state", config.StoreOptions{Offline: true})
	require.NoError(t, err)
	s := new(app.Service)
	require.NoError(t, s.StartManaged(context.Background(), store))
	t.Cleanup(func() { assert.NoError(t, s.Close()) })
	// Password verification performs 600,000 PBKDF2 iterations; race-instrumented
	// Linux CI can exceed one second even when the local service is healthy.
	client := http.Client{Timeout: 10 * time.Second}
	base := "http://" + s.Addresses().Admin
	login, err := client.Post(base+"/session", "application/json", strings.NewReader(`{"password":"admin"}`))
	require.NoError(t, err)
	login.Body.Close()
	require.Equal(t, http.StatusOK, login.StatusCode)
	secret, err := store.ActiveSecret(config.AdminSecretName)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(secret), "pbkdf2-sha256$600000$"))
	for _, route := range []string{"/health/live", "/health/ready"} {
		r, err := client.Get(base + route)
		require.NoError(t, err)
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		assert.Equal(t, http.StatusOK, r.StatusCode)
	}
	r, err := client.Get(base + "/api/v1/summary")
	require.NoError(t, err)
	r.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, r.StatusCode)
	r, err = client.Get(base + "/")
	require.NoError(t, err)
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	r.Body.Close()
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, r.StatusCode)
	assert.True(t, strings.Contains(strings.ToLower(string(b)), "<!doctype html>"))
	var output, stderr bytes.Buffer
	assert.Zero(t, cli.Run(context.Background(), []string{"--socket", s.Addresses().Control, "diagnostics"}, &output, &stderr), stderr.String())
	var diagnostics map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &diagnostics))
	assert.Equal(t, []any{}, diagnostics["dns_addresses"], "loopback-only DNS has no address for other devices")
	q := new(dns.Msg)
	q.SetQuestion("1.0.0.10.in-addr.arpa.", dns.TypePTR)
	dnsClient := dns.Client{Timeout: time.Second}
	answer, _, err := dnsClient.Exchange(q, s.Addresses().DNS[0])
	require.NoError(t, err)
	assert.Equal(t, dns.RcodeNameError, answer.Rcode)
	now := time.Now().UTC().Truncate(time.Microsecond)
	window := url.Values{"from": {now.Add(-time.Hour).Format(time.RFC3339Nano)}, "to": {now.Add(time.Minute).Format(time.RFC3339Nano)}}.Encode()
	require.Eventually(t, func() bool {
		output.Reset()
		stderr.Reset()
		if cli.Run(context.Background(), []string{"--socket", s.Addresses().Control, "queries", "--query", window}, &output, &stderr) != 0 {
			return false
		}
		return strings.Contains(output.String(), "1.0.0.10.in-addr.arpa")
	}, 3*time.Second, 20*time.Millisecond, "query polling failed: %s", &stderr)
	output.Reset()
	stderr.Reset()
	require.Zero(t, cli.Run(t.Context(), []string{"--socket", s.Addresses().Control, "catalog"}, &output, &stderr), stderr.String())
	assert.Contains(t, output.String(), `"id":"stevenblack-unified"`)
	input, err := os.CreateTemp(dir, "password-input")
	require.NoError(t, err)
	t.Cleanup(func() { input.Close() })
	_, err = input.WriteString(`{"password":"new"}`)
	require.NoError(t, err)
	_, err = input.Seek(0, 0)
	require.NoError(t, err)
	previous := os.Stdin
	os.Stdin = input
	t.Cleanup(func() { os.Stdin = previous })
	output.Reset()
	stderr.Reset()
	require.Zero(t, cli.Run(t.Context(), []string{"--socket", s.Addresses().Control, "password", "@-"}, &output, &stderr), stderr.String())
	os.Stdin = previous
	assert.NotContains(t, output.String(), "new")
	oldSession, err := http.NewRequest("GET", base+"/api/v1/catalog", nil)
	require.NoError(t, err)
	oldSession.AddCookie(login.Cookies()[0])
	r, err = client.Do(oldSession)
	require.NoError(t, err)
	r.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, r.StatusCode)
	for password, status := range map[string]int{"admin": 401, "new": 200} {
		r, err := client.Post(base+"/session", "application/json", strings.NewReader(`{"password":"`+password+`"}`))
		require.NoError(t, err)
		r.Body.Close()
		assert.Equal(t, status, r.StatusCode)
	}
	changed, err := store.ActiveSecret(config.AdminSecretName)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	reopened, err := config.OpenStore(t.Context(), path, path+".state", config.StoreOptions{Offline: true})
	require.NoError(t, err)
	restarted := new(app.Service)
	require.NoError(t, restarted.StartManaged(t.Context(), reopened))
	t.Cleanup(func() { assert.NoError(t, restarted.Close()) })
	afterRestart, err := reopened.ActiveSecret(config.AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, changed, afterRestart)
	r, err = client.Post("http://"+restarted.Addresses().Admin+"/session", "application/json", strings.NewReader(`{"password":"new"}`))
	require.NoError(t, err)
	r.Body.Close()
	assert.Equal(t, http.StatusOK, r.StatusCode)
}
