package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/richkeenan/dimsum/internal/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoginExplainsFailures(t *testing.T) {
	for _, tc := range []struct {
		status  int
		message string
	}{
		{401, "Login rejected"}, {403, "not allowed"}, {404, "does not appear to provide"},
		{429, "Too many login attempts"}, {503, "not ready"}, {302, "redirect"}, {200, "unexpected response"},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("not JSON"))
			}))
			defer server.Close()
			password := filepath.Join(t.TempDir(), "password")
			require.NoError(t, os.WriteFile(password, []byte("fixture-password"), 0600))
			var out, stderr bytes.Buffer
			assert.NotZero(t, cli.Run(t.Context(), []string{"login", "--server", server.URL, "--password-file", password}, &out, &stderr))
			assert.Contains(t, stderr.String(), "Logging in to "+server.URL)
			assert.Contains(t, stderr.String(), tc.message)
			assert.NotContains(t, stderr.String(), "POST /session")
			assert.NotContains(t, stderr.String(), "fixture-password")
		})
	}
}

func TestLoginConnectionAndCancellationMessages(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	password := filepath.Join(t.TempDir(), "password")
	require.NoError(t, os.WriteFile(password, []byte("fixture-password"), 0600))
	args := []string{"login", "--server", server.URL, "--password-file", password}
	var out, stderr bytes.Buffer
	assert.NotZero(t, cli.Run(t.Context(), args, &out, &stderr))
	assert.Contains(t, stderr.String(), "Could not connect to "+server.URL)
	assert.Contains(t, stderr.String(), "--server")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stderr.Reset()
	assert.NotZero(t, cli.Run(ctx, args, &out, &stderr))
	assert.Contains(t, stderr.String(), "Login cancelled.")
	assert.NotContains(t, stderr.String(), "context canceled")
}

func TestControlWithoutLoginOffersNextStep(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DIMSUM_CONTROL_SOCKET", "")
	var out, stderr bytes.Buffer
	assert.NotZero(t, cli.Run(t.Context(), []string{"control", "settings"}, &out, &stderr))
	assert.Contains(t, stderr.String(), "Not logged in")
	assert.Contains(t, stderr.String(), "dimsum login")
	assert.NotContains(t, stderr.String(), "no such file")
}

func TestLoginFailureDoesNotSaveCredentials(t *testing.T) {
	for _, status := range []int{401, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", home)
			var leaked atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
			defer destination.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", destination.URL)
				w.WriteHeader(status)
			}))
			defer server.Close()
			password := filepath.Join(t.TempDir(), "password")
			require.NoError(t, os.WriteFile(password, []byte("fixture-password"), 0600))
			var out, stderr bytes.Buffer
			assert.NotEqual(t, 0, cli.Run(t.Context(), []string{"login", "--server", server.URL, "--password-file", password}, &out, &stderr))
			assert.Zero(t, leaked.Load())
			assert.NotContains(t, stderr.String(), "fixture-password")
			_, err := os.Stat(filepath.Join(home, "dimsum", "credentials.json"))
			assert.True(t, os.IsNotExist(err))
		})
	}
}

func TestSavedConnectionRejectsRedirectsAndUnsafeCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("DIMSUM_CONTROL_SOCKET", "")
	var leaked atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer server.Close()
	dir := filepath.Join(home, "dimsum")
	require.NoError(t, os.Mkdir(dir, 0700))
	path := filepath.Join(dir, "credentials.json")
	data, err := json.Marshal(map[string]string{"server": server.URL, "id": "fixture-id", "token": "fixture-token"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
	var out, stderr bytes.Buffer
	assert.Equal(t, 4, cli.Run(t.Context(), []string{"control", "settings"}, &out, &stderr))
	assert.Zero(t, leaked.Load())
	require.NoError(t, os.Chmod(path, 0644))
	stderr.Reset()
	assert.Equal(t, 3, cli.Run(t.Context(), []string{"control", "settings"}, &out, &stderr))
	assert.Contains(t, stderr.String(), "owner-only")
	require.NoError(t, os.Remove(path))
	target := filepath.Join(t.TempDir(), "credentials")
	require.NoError(t, os.WriteFile(target, data, 0600))
	require.NoError(t, os.Symlink(target, path))
	stderr.Reset()
	assert.Equal(t, 3, cli.Run(t.Context(), []string{"control", "settings"}, &out, &stderr))
	assert.Contains(t, stderr.String(), "regular file")
}

func TestLogoutRevokedTokenAndUnavailableServer(t *testing.T) {
	for _, status := range []int{401, 404, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", home)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			dir := filepath.Join(home, "dimsum")
			require.NoError(t, os.Mkdir(dir, 0700))
			path := filepath.Join(dir, "credentials.json")
			data, err := json.Marshal(map[string]string{"server": server.URL, "id": "fixture-id", "token": "fixture-token"})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, data, 0600))
			var out, stderr bytes.Buffer
			code := cli.Run(t.Context(), []string{"logout"}, &out, &stderr)
			_, err = os.Stat(path)
			if status == 503 {
				assert.NotZero(t, code)
				assert.NoError(t, err)
				assert.Contains(t, stderr.String(), "Your saved connection has been kept")
			} else {
				assert.Zero(t, code, stderr.String())
				assert.True(t, os.IsNotExist(err))
			}
		})
	}
}
