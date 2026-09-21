package admin_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenPersistenceAndRevocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets", "api-tokens.json")
	store, err := admin.OpenTokenStore(path)
	require.NoError(t, err)
	created, err := store.Create("automation")
	require.NoError(t, err)
	assert.True(t, store.Authenticate(created.Token))
	assert.False(t, store.Authenticate(created.Token+"x"))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(data), created.Token)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	store, err = admin.OpenTokenStore(path)
	require.NoError(t, err)
	assert.True(t, store.Authenticate(created.Token))
	listed, err := json.Marshal(store.List())
	require.NoError(t, err)
	assert.NotContains(t, string(listed), "hash")
	assert.NotContains(t, string(listed), created.Token)
	require.NoError(t, store.Revoke(created.ID))
	store, err = admin.OpenTokenStore(path)
	require.NoError(t, err)
	assert.False(t, store.Authenticate(created.Token))
	assert.Empty(t, store.List())
}

func TestTokenStoreRejectsUnsafeAndCorruptFiles(t *testing.T) {
	for i, body := range []string{"", "null", "{}", "{", `{"version":1,"items":[{}]}`, `{"version":99,"items":[]}`, `{"version":1,"items":[]}` + strings.Repeat(" ", 64<<10) + "invalid"} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			path := filepath.Join(privateTokenDir(t), "tokens.json")
			require.NoError(t, os.WriteFile(path, []byte(body), 0600))
			_, err := admin.OpenTokenStore(path)
			assert.Error(t, err)
		})
	}
	dir := privateTokenDir(t)
	target := filepath.Join(dir, "target")
	require.NoError(t, os.WriteFile(target, []byte(`{"version":1,"items":[]}`), 0644))
	_, err := admin.OpenTokenStore(target)
	assert.Error(t, err)
	require.NoError(t, os.Chmod(target, 0600))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(target, link))
	_, err = admin.OpenTokenStore(link)
	assert.Error(t, err)
	require.NoError(t, os.Symlink(dir, filepath.Join(dir, "linked-dir")))
	_, err = admin.OpenTokenStore(filepath.Join(dir, "linked-dir", "new.json"))
	assert.Error(t, err)
}

func TestTokenBoundsAndConcurrentMutations(t *testing.T) {
	path := filepath.Join(privateTokenDir(t), "tokens.json")
	store, err := admin.OpenTokenStore(path)
	require.NoError(t, err)
	for _, name := range []string{"", " ", strings.Repeat("a", 81)} {
		_, err = store.Create(name)
		assert.Error(t, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, err := store.Create(fmt.Sprint(i)); results <- err }(i)
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	assert.Len(t, store.List(), 32)
	_, err = store.Create("overflow")
	assert.Error(t, err)
	reopened, err := admin.OpenTokenStore(path)
	require.NoError(t, err)
	assert.Equal(t, store.List(), reopened.List())
}

func TestTokenMutationFailureDoesNotPublishOrFollowSymlinks(t *testing.T) {
	dir := privateTokenDir(t)
	path := filepath.Join(dir, "tokens.json")
	store, err := admin.OpenTokenStore(path)
	require.NoError(t, err)
	created, err := store.Create("retained")
	require.NoError(t, err)
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	target := filepath.Join(dir, "original.json")
	require.NoError(t, os.Rename(path, target))
	require.NoError(t, os.Symlink(target, path))
	_, err = store.Create("must-fail")
	assert.Error(t, err)
	assert.Error(t, store.Revoke(created.ID))
	assert.True(t, store.Authenticate(created.Token))
	assert.Len(t, store.List(), 1)
	unchanged, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, original, unchanged)
}

func TestMCPAdmissionBodyAndTimeBounds(t *testing.T) {
	store, err := admin.OpenTokenStore("")
	require.NoError(t, err)
	created, err := store.Create("bounds")
	require.NoError(t, err)
	entered := make(chan struct{}, 16)
	release := make(chan struct{})
	server := admin.New(nil, admin.Options{Tokens: store, AllowedHosts: []string{"admin.test"}, RequestTimeout: time.Second, MCP: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Test-Body") == "yes" {
			_, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(413)
				return
			}
			w.WriteHeader(204)
			return
		}
		if _, ok := r.Context().Deadline(); !ok {
			w.WriteHeader(500)
			return
		}
		entered <- struct{}{}
		<-release
		w.WriteHeader(204)
	})})
	call := func(body string, readBody bool) int {
		r := httptest.NewRequest("POST", "http://admin.test/mcp", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+created.Token)
		r.Header.Set("Mcp-Session-Id", "existing-session")
		if readBody {
			r.Header.Set("Test-Body", "yes")
		}
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w.Code
	}
	assert.Equal(t, 413, call(strings.Repeat("x", (1<<20)+1), true))
	done := make(chan int, 16)
	for i := 0; i < 16; i++ {
		go func() { done <- call("", false) }()
	}
	for i := 0; i < 16; i++ {
		<-entered
	}
	assert.Equal(t, 429, call("", false))
	close(release)
	for i := 0; i < 16; i++ {
		assert.Equal(t, 204, <-done)
	}
	require.NoError(t, store.Revoke(created.ID))
	assert.Equal(t, 401, call("", false))
}

func privateTokenDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0700))
	return dir
}

func TestBearerMCPAndTokenRoutes(t *testing.T) {
	service, _, _ := fixture(t)
	store, err := admin.OpenTokenStore("")
	require.NoError(t, err)
	created, err := store.Create("agent")
	require.NoError(t, err)
	hash, err := admin.HashPassword("a-long-test-password")
	require.NoError(t, err)
	server := admin.New(service, admin.Options{Tokens: store, PasswordHash: hash, AllowedHosts: []string{"admin.test"}, OpenAPIJSON: []byte(`{"openapi":"3.1.0"}`), OpenAPIYAML: []byte("openapi: 3.1.0"), MCP: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })})
	h := server.Handler()
	login := request(h, "POST", "/session", `{"password":"a-long-test-password"}`)
	require.Equal(t, 200, login.Code)
	cookie := login.Result().Cookies()[0]
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	require.NoError(t, json.Unmarshal(login.Body.Bytes(), &session))
	csrfRequest := httptest.NewRequest("POST", "http://admin.test/api/v1/tokens", strings.NewReader(`{"name":"browser"}`))
	csrfRequest.AddCookie(cookie)
	csrfRequest.Header.Set("Origin", "http://admin.test")
	csrfRequest.Header.Set("X-CSRF-Token", session.CSRF)
	csrfResponse := httptest.NewRecorder()
	h.ServeHTTP(csrfResponse, csrfRequest)
	assert.Equal(t, 200, csrfResponse.Code)
	call := func(method, path, bearer, origin, host string, withCookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://admin.test"+path, strings.NewReader(`{"name":"second"}`))
		r.Host = host
		if bearer != "" {
			r.Header.Set("Authorization", bearer)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if withCookie {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	auth := "Bearer " + created.Token
	assert.Equal(t, 200, call("GET", "/api/v1/tokens", auth, "", "admin.test", false).Code)
	assert.Equal(t, 200, call("POST", "/api/v1/tokens", auth, "", "admin.test", false).Code)
	assert.Equal(t, 401, call("GET", "/api/v1/tokens", "Bearer invalid", "", "admin.test", true).Code)
	assert.Equal(t, 401, call("GET", "/api/v1/tokens", "Basic invalid", "", "admin.test", true).Code)
	assert.Equal(t, 403, call("POST", "/api/v1/tokens", "", "", "admin.test", true).Code)
	assert.Equal(t, 403, call("GET", "/api/v1/tokens", auth, "http://evil.test", "admin.test", false).Code)
	assert.Equal(t, 403, call("POST", "/mcp", auth, "http://evil.test", "admin.test", false).Code)
	assert.Equal(t, 403, call("GET", "/api/v1/tokens", auth, "", "evil.test", false).Code)
	assert.Equal(t, 401, call("POST", "/mcp", "", "", "admin.test", true).Code)
	for _, method := range []string{"GET", "POST", "DELETE"} {
		assert.Equal(t, 204, call(method, "/mcp", auth, "", "admin.test", false).Code)
	}
	assert.Equal(t, 200, call("GET", "/api/v1/openapi.json", auth, "", "admin.test", false).Code)
	assert.Equal(t, 200, call("GET", "/api/v1/openapi.yaml", auth, "", "admin.test", false).Code)
	assert.Equal(t, 200, call("DELETE", "/api/v1/tokens/"+created.ID, auth, "", "admin.test", false).Code)
	assert.Equal(t, 401, call("POST", "/mcp", auth, "", "admin.test", false).Code)
	assert.Equal(t, 200, request(server.LocalHandler(), "GET", "/api/v1/tokens", "").Code)
	assert.Equal(t, 200, request(server.LocalHandler(), "POST", "/api/v1/tokens", `{"name":"local"}`).Code)
	assert.Equal(t, 401, request(server.LocalHandler(), "POST", "/mcp", `{}`).Code)
}
