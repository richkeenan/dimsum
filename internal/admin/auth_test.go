package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthenticationOriginCSRFHostExpiryAndLogout(t *testing.T) {
	service, _, _ := fixture(t)
	hash, e := admin.HashPassword("a-long-test-password")
	require.NoError(t, e)
	now := time.Now().UTC()
	server := admin.New(service, admin.Options{PasswordHash: hash, AllowedHosts: []string{"admin.test"}, SessionTTL: time.Minute, Now: func() time.Time { return now }})
	h := server.Handler()
	assert.Equal(t, 401, request(h, "GET", "/api/v1/settings", "").Code)
	w := request(h, "POST", "/session", `{"password":"wrong"}`)
	assert.Equal(t, 401, w.Code)
	w = request(h, "POST", "/session", `{"password":"a-long-test-password"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	var login struct {
		CSRF string `json:"csrf_token"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &login))
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	cookie := cookies[0]
	assert.True(t, cookie.HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	assert.NotEmpty(t, login.CSRF)
	call := func(method, path, origin, csrf, host string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://admin.test"+path, strings.NewReader(`{}`))
		r.Host = host
		r.AddCookie(cookie)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	assert.Equal(t, 200, call("GET", "/api/v1/settings", "", "", "admin.test").Code)
	assert.Equal(t, 403, call("GET", "/api/v1/settings", "", "", "evil.test").Code)
	assert.Equal(t, 403, call("GET", "/api/v1/settings", "http://evil.test", "", "admin.test").Code)
	assert.Equal(t, 403, call("PATCH", "/api/v1/settings", "http://admin.test", "", "admin.test").Code)
	assert.Equal(t, 403, call("PATCH", "/api/v1/settings", "", login.CSRF, "admin.test").Code)
	assert.Equal(t, 422, call("PATCH", "/api/v1/settings", "http://admin.test", login.CSRF, "admin.test").Code)
	now = now.Add(time.Minute)
	assert.Equal(t, 401, call("GET", "/api/v1/settings", "", "", "admin.test").Code)
	now = now.Add(time.Minute)
	w = request(h, "POST", "/session", `{"password":"a-long-test-password"}`)
	require.Equal(t, 200, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &login))
	cookie = w.Result().Cookies()[0]
	assert.Equal(t, 200, call("DELETE", "/session", "http://admin.test", login.CSRF, "admin.test").Code)
	assert.Equal(t, 401, call("GET", "/api/v1/settings", "", "", "admin.test").Code)
	w = request(h, "POST", "/session", `{"password":"a-long-test-password"}`)
	require.Equal(t, 200, w.Code)
	cookie = w.Result().Cookies()[0]
	server.SetPasswordHash("")
	assert.Equal(t, 401, call("GET", "/api/v1/settings", "", "", "admin.test").Code)
	assert.Equal(t, 503, request(h, "POST", "/session", `{"password":"a-long-test-password"}`).Code)
}

func TestLoginRateLimitSecureCookieAndBootstrap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "admin.hash")
	require.NoError(t, admin.BootstrapPassword(path, "a-long-test-password"))
	assert.Error(t, admin.BootstrapPassword(path, "another-long-password"))
	hash, e := admin.LoadPasswordHash(path)
	require.NoError(t, e)
	service, _, _ := fixture(t)
	h := admin.New(service, admin.Options{PasswordHash: hash, AllowedHosts: []string{"admin.test"}, SecureCookies: true}).Handler()
	w := request(h, "POST", "/session", `{"password":"a-long-test-password"}`)
	require.Equal(t, 200, w.Code)
	assert.True(t, w.Result().Cookies()[0].Secure)
	for i := 0; i < 4; i++ {
		assert.Equal(t, 401, request(h, "POST", "/session", `{"password":"wrong"}`).Code)
	}
	assert.Equal(t, 429, request(h, "POST", "/session", `{"password":"wrong"}`).Code)
	require.NoError(t, os.Chmod(path, 0644))
	_, e = admin.LoadPasswordHash(path)
	assert.Error(t, e)
}

func TestStrictBodyAndSecretSeparation(t *testing.T) {
	service, store, path := fixture(t)
	h := admin.New(service, admin.Options{}).LocalHandler()
	for _, body := range []string{`{"unexpected":true}`, `{} {}`, strings.Repeat(" ", 1<<20) + `{}`} {
		w := request(h, "PATCH", "/api/v1/settings", body)
		assert.Equal(t, 400, w.Code)
	}
	secret := "never-expose-this-password"
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), "admin.hash"), []byte(secret), 0600))
	assert.NotContains(t, request(h, "GET", "/api/v1/settings", "").Body.String(), secret)
	w := request(h, "POST", "/api/v1/lists", mutation(t, store, map[string]any{"item": map[string]any{"id": "private-list", "url": "https://example.invalid/list?token=" + secret, "enabled": false, "dialect": "domains", "domain_kind": "exact"}}))
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.NotContains(t, request(h, "GET", "/api/v1/settings", "").Body.String(), secret)
	assert.NotContains(t, request(h, "GET", "/api/v1/lists", "").Body.String(), secret)
	b, e := os.ReadFile(path)
	require.NoError(t, e)
	assert.Contains(t, string(b), secret)
}
