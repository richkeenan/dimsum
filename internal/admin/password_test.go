package admin_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPasswordByteBoundaries(t *testing.T) {
	for _, password := range []string{"admin", " ", strings.Repeat("é", 512)} {
		hash, err := admin.HashPassword(password)
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(hash, "pbkdf2-sha256$600000$"))
	}
	for _, password := range []string{"", strings.Repeat("é", 513)} {
		_, err := admin.HashPassword(password)
		assert.Error(t, err)
	}
}

func TestPasswordChangeRevokesSessionsAndPersists(t *testing.T) {
	service, store, _ := fixture(t)
	hash, err := admin.HashPassword("original-password")
	require.NoError(t, err)
	server := admin.New(service, admin.Options{PasswordHash: hash, AllowedHosts: []string{"admin.test"}})
	h := server.Handler()
	login := request(h, "POST", "/session", `{"password":"original-password"}`)
	require.Equal(t, 200, login.Code)
	var session map[string]string
	require.NoError(t, json.Unmarshal(login.Body.Bytes(), &session))
	change := func(body, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://admin.test/api/v1/password", strings.NewReader(body))
		r.AddCookie(login.Result().Cookies()[0])
		r.Header.Set("Origin", "http://admin.test")
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	assert.Equal(t, 401, request(h, "POST", "/api/v1/password", `{"password":"admin"}`).Code)
	assert.Equal(t, 403, change(`{"password":"admin"}`, "wrong").Code)
	for _, body := range []string{`{}`, `{"password":""}`, `{"password":null}`, `{"password":"admin","extra":true}`, `{"password":"` + strings.Repeat("a", 1025) + `"}`} {
		assert.Equal(t, 400, change(body, session["csrf_token"]).Code)
	}
	w := change(`{"password":"admin"}`, session["csrf_token"])
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, 401, change(`{"password":"next"}`, session["csrf_token"]).Code)
	assert.Equal(t, 401, request(h, "POST", "/session", `{"password":"original-password"}`).Code)
	assert.Equal(t, 200, request(h, "POST", "/session", `{"password":"admin"}`).Code)
	secret, err := store.ActiveSecret("admin.hash")
	require.NoError(t, err)
	restarted := admin.New(service, admin.Options{PasswordHash: strings.TrimSpace(string(secret)), AllowedHosts: []string{"admin.test"}})
	assert.Equal(t, 200, request(restarted.Handler(), "POST", "/session", `{"password":"admin"}`).Code)
	assert.NotEmpty(t, store.Snapshot().Config().Admin.SecretGeneration)
}

func TestCatalogEndpoint(t *testing.T) {
	service, _, _ := fixture(t)
	w := request(admin.New(service, admin.Options{}).LocalHandler(), "GET", "/api/v1/catalog", "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var response struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Len(t, response.Items, len(catalog.Entries()))
	for i, e := range catalog.Entries() {
		assert.Equal(t, e.ID, response.Items[i]["id"])
		assert.Equal(t, e.Available, response.Items[i]["available"])
		assert.Equal(t, e.UnavailableReason, response.Items[i]["unavailable_reason"])
		assert.Contains(t, response.Items[i], "domain_kind")
		assert.Equal(t, string(e.Source().DomainKind), response.Items[i]["domain_kind"])
		assert.Equal(t, string(e.Dialect), response.Items[i]["dialect"])
		assert.Equal(t, e.DefaultEnabled, response.Items[i]["default_enabled"])
	}
}
