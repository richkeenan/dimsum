package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/app"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBootstrapOneTimeSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	password := filepath.Join(dir, "password")
	require.NoError(t, os.WriteFile(path, []byte("version: 1\ndns:\n  listen: [127.0.0.1:5353]\nadmin:\n  listen: 127.0.0.1:8080\npaths:\n  data_dir: data\n  secrets_dir: secrets\n"), 0600))
	require.NoError(t, os.WriteFile(password, []byte("test-password-at-least-twelve\n"), 0600))
	var out, stderr bytes.Buffer
	args := []string{"bootstrap", "-config", path, "-password-file", password}
	require.Zero(t, run(context.Background(), args, &out, &stderr), stderr.String())
	hash, err := admin.LoadPasswordHash(filepath.Join(dir, "secrets", "admin.hash"))
	require.NoError(t, err)
	assert.Contains(t, hash, "pbkdf2-sha256$")
	assert.Equal(t, 1, run(context.Background(), args, &out, &stderr))
	unchanged, err := admin.LoadPasswordHash(filepath.Join(dir, "secrets", "admin.hash"))
	require.NoError(t, err)
	assert.Equal(t, hash, unchanged)
}

func TestBootstrapGeneratedPasswordIsOneTimeAndUsable(t *testing.T) {
	var passwords []string
	for range 2 {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte("version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [127.0.0.1:9]\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: data\n  secrets_dir: secrets\n"), 0600))
		var out, stderr bytes.Buffer
		args := []string{"bootstrap", "-config", path, "-generate"}
		require.Zero(t, run(t.Context(), args, &out, &stderr), stderr.String())
		var result struct {
			Created  bool   `json:"created"`
			Password string `json:"password"`
		}
		require.NoError(t, json.Unmarshal(out.Bytes(), &result))
		assert.True(t, result.Created)
		assert.GreaterOrEqual(t, len(result.Password), 32)
		passwords = append(passwords, result.Password)
		hash, err := admin.LoadPasswordHash(filepath.Join(dir, "secrets", "admin.hash"))
		require.NoError(t, err)
		assert.NotContains(t, hash, result.Password)
		out.Reset()
		assert.Equal(t, 1, run(t.Context(), args, &out, &stderr))
		assert.Empty(t, out.String(), "a failed/repeated bootstrap must not disclose another password")
		after, err := admin.LoadPasswordHash(filepath.Join(dir, "secrets", "admin.hash"))
		require.NoError(t, err)
		assert.Equal(t, hash, after)
		out.Reset()
		require.Zero(t, run(t.Context(), append(args, "-if-needed"), &out, &stderr), stderr.String())
		assert.JSONEq(t, `{"created":false}`, out.String())
		after, err = admin.LoadPasswordHash(filepath.Join(dir, "secrets", "admin.hash"))
		require.NoError(t, err)
		assert.Equal(t, hash, after)
		store, err := config.OpenStore(t.Context(), path, path+".state", config.StoreOptions{Offline: true})
		require.NoError(t, err)
		service := new(app.Service)
		require.NoError(t, service.StartManaged(t.Context(), store))
		t.Cleanup(func() { assert.NoError(t, service.Close()) })
		client := http.Client{Timeout: 10 * time.Second}
		response, err := client.Post("http://"+service.Addresses().Admin+"/session", "application/json", strings.NewReader(`{"password":"`+result.Password+`"}`))
		require.NoError(t, err)
		response.Body.Close()
		assert.Equal(t, http.StatusOK, response.StatusCode)
		require.NoError(t, service.Close())
	}
	assert.NotEqual(t, passwords[0], passwords[1])
}

func TestBootstrapIfNeededRepairsConfigurationWithoutCredential(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: 1\ndns:\n  listen: [127.0.0.1:0]\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: data\n  secrets_dir: secrets\n"), 0600))
	var out, stderr bytes.Buffer
	require.Zero(t, run(t.Context(), []string{"bootstrap", "-config", path, "-generate", "-if-needed"}, &out, &stderr), stderr.String())
	assert.Contains(t, out.String(), `"password":`)
	_, err := admin.LoadPasswordHash(filepath.Join(dir, "secrets", "admin.hash"))
	require.NoError(t, err)
}

func TestBootstrapIfNeededPreservesReferencedGeneration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: 1\ndns:\n  listen: [127.0.0.1:0]\nadmin:\n  listen: 127.0.0.1:0\n  secret_generation: deadbeef\npaths:\n  data_dir: data\n  secrets_dir: secrets\n"), 0600))
	var out, stderr bytes.Buffer
	args := []string{"bootstrap", "-config", path, "-generate", "-if-needed"}
	assert.Equal(t, 1, run(t.Context(), args, &out, &stderr), "a missing referenced generation must not fall back to a new credential")
	assert.Empty(t, out.String())
	generation := filepath.Join(dir, "secrets", "generations", "deadbeef")
	require.NoError(t, os.MkdirAll(generation, 0700))
	credential := filepath.Join(generation, "admin.hash")
	require.NoError(t, admin.BootstrapPassword(credential, "fixture-password"))
	before, err := os.ReadFile(credential)
	require.NoError(t, err)
	require.Zero(t, run(t.Context(), args, &out, &stderr), stderr.String())
	assert.JSONEq(t, `{"created":false}`, out.String())
	after, err := os.ReadFile(credential)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	_, err = os.Stat(filepath.Join(dir, "secrets", "admin.hash"))
	assert.True(t, os.IsNotExist(err))
}
