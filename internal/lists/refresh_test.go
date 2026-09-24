package lists

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRefreshRetentionConditionalOffline(t *testing.T) {
	body, status := "ads.test\nbad..test\nother.test\n", 200
	conditional := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conditional = r.Header.Get("If-None-Match") == "v1"
		w.Header().Set("ETag", "v1")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	spec := Subscription{ID: "one", URL: srv.URL, Dialect: Domains, DomainKind: policy.Exact, Enabled: true}
	f := NewFetcher(srv.Client())
	dir := t.TempDir()
	first, err := f.Refresh(context.Background(), dir, spec, false)
	require.NoError(t, err)
	require.Len(t, first.Rules, 2)
	assert.Contains(t, first.Warning, "skipped 1 invalid domain")
	status = 304
	second, err := f.Refresh(context.Background(), dir, spec, false)
	require.NoError(t, err)
	assert.True(t, conditional)
	assert.Equal(t, first.Rules, second.Rules)
	assert.Equal(t, first.Warning, second.Warning)
	for _, bad := range []string{"", "@@||ads.test^$bad\n", "ads.test\n"} {
		status = 200
		body = bad
		retained, err := f.Refresh(context.Background(), dir, spec, false)
		require.NoError(t, err)
		assert.NotEmpty(t, retained.Warning)
		assert.Equal(t, first.Rules, retained.Rules)
	}
	srv.Close()
	offline, err := f.Refresh(context.Background(), dir, spec, true)
	require.NoError(t, err)
	assert.Equal(t, first.Rules, offline.Rules)
	assert.Contains(t, offline.Warning, "skipped 1 invalid domain")
	spec.URL += "/different"
	_, err = f.Refresh(context.Background(), dir, spec, true)
	assert.Error(t, err, "identity changes must not reuse old rules")
}

func TestFetchBoundsAndArtifactCorruption(t *testing.T) {
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	_, err := z.Write(bytes.Repeat([]byte("a"), 4096))
	require.NoError(t, err)
	require.NoError(t, z.Close())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressed.Bytes())
	}))
	defer srv.Close()
	f := NewFetcher(srv.Client())
	f.MaxExpanded = 100
	_, err = f.Refresh(context.Background(), t.TempDir(), Subscription{ID: "bomb", URL: srv.URL, Dialect: Domains, DomainKind: policy.Exact, Enabled: true}, false)
	require.Error(t, err)
	p := filepath.Join(t.TempDir(), "artifact")
	require.NoError(t, WriteArtifact(p, []byte("good")))
	b, err := ReadArtifact(p, 100)
	require.NoError(t, err)
	assert.Equal(t, "good", string(b))
	require.NoError(t, os.WriteFile(p, []byte("corrupt"), 0600))
	_, err = ReadArtifact(p, 100)
	assert.Error(t, err)
}

func TestBuiltinWorkListLoadsWithoutNetworkOrArtifacts(t *testing.T) {
	for _, offline := range []bool{false, true} {
		t.Run(fmt.Sprint(offline), func(t *testing.T) {
			// A nil fetcher and an unusable artifact path catch accidental HTTP or disk access.
			var f *Fetcher
			dir := filepath.Join(t.TempDir(), "not-a-directory")
			require.NoError(t, os.WriteFile(dir, []byte("fixture"), 0600))
			spec := Subscription{ID: "work", URL: "builtin://work-compatibility", Dialect: Adblock, DomainKind: policy.Suffix, Enabled: true}
			v, err := f.Refresh(t.Context(), dir, spec, offline)
			require.NoError(t, err)
			require.NotEmpty(t, v.Rules)
			assert.NotEmpty(t, v.SHA256)
			assert.Empty(t, v.Warning)
			for _, rule := range v.Rules {
				assert.Equal(t, policy.SubscriptionAllow, rule.Class)
				assert.Equal(t, policy.Suffix, rule.Kind)
				assert.Equal(t, "work", rule.SourceID)
			}
		})
	}
}
