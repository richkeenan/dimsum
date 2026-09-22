package app_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/richkeenan/dimsum/internal/app"
	"github.com/richkeenan/dimsum/internal/cli"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCLILoginControlLogout(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DIMSUM_CONTROL_SOCKET", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "dimsum.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [127.0.0.1:9]\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: data\n  secrets_dir: secrets\n"), 0600))
	store, err := config.OpenStore(t.Context(), path, path+".state", config.StoreOptions{Offline: true})
	require.NoError(t, err)
	s := new(app.Service)
	require.NoError(t, s.StartManaged(t.Context(), store))
	t.Cleanup(func() { assert.NoError(t, s.Close()) })
	password := filepath.Join(dir, "password")
	require.NoError(t, os.WriteFile(password, []byte("admin\n"), 0600))
	run := func(args ...string) string {
		var out, stderr bytes.Buffer
		require.Equal(t, 0, cli.Run(t.Context(), args, &out, &stderr), stderr.String())
		return out.String()
	}
	base := "http://" + s.Addresses().Admin
	run("login", "--server", base, "--password-file", password)
	credential := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "dimsum", "credentials.json")
	raw, err := os.ReadFile(credential)
	require.NoError(t, err)
	var saved struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(raw, &saved))
	require.NotEmpty(t, saved.Token)
	assert.NotContains(t, string(raw), "admin")
	info, err := os.Stat(credential)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	run("control", "settings")
	run("control", "password", `{"password":"changed-fixture"}`)
	run("logout")
	_, err = os.Stat(credential)
	assert.True(t, os.IsNotExist(err))
	req, err := http.NewRequestWithContext(t.Context(), "GET", base+"/api/v1/settings", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+saved.Token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	var out, stderr bytes.Buffer
	assert.NotEqual(t, 0, cli.Run(t.Context(), []string{"control", "settings"}, &out, &stderr))
	assert.Contains(t, stderr.String(), "dimsum login")
}
