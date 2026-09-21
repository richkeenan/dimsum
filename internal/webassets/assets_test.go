package webassets

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedSPA(t *testing.T) {
	h := Handler()
	for _, route := range []string{"/", "/queries", "/settings", "/diagnostics"} {
		t.Run(route, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, route, nil))
			require.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), `<div id="root">`)
			assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
			assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
		})
	}
}

func TestReservedNamespacesAndMissingAssets(t *testing.T) {
	for _, route := range []string{"/api/v1/absent", "/api", "/session", "/health/live", "/debug/pprof", "/metrics", "/assets", "/assets/missing.js", "/unknown", "/../index.html"} {
		w := httptest.NewRecorder()
		Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, route, nil))
		assert.Equal(t, http.StatusNotFound, w.Code, route)
		assert.NotContains(t, w.Body.String(), `<div id="root">`, route)
	}
}

func TestAssetCachingAndHead(t *testing.T) {
	files, err := fs.Glob(assets, "dist/assets/*.js")
	require.NoError(t, err)
	require.NotEmpty(t, files, "run the frontend production build")
	url := strings.TrimPrefix(files[0], "dist")
	h := Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "public, max-age=31536000, immutable", w.Header().Get("Cache-Control"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("If-None-Match", w.Header().Get("ETag"))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotModified, w.Code)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodHead, url, nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String())
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/queries", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
