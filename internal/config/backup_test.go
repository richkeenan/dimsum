package config

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const backupSample = "# owned by operator\nversion: 1 # schema\ndns:\n  listen: [\"127.0.0.1:0\"]\nadmin:\n  listen: '127.0.0.1:0' # private\npaths:\n  data_dir:   './data'\n  secrets_dir: './secrets'\n"

// Syntactically valid PBKDF2 records with synthetic, non-production material.
func fixtureSecret(seed byte) []byte {
	return []byte("pbkdf2-sha256$600000$" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 16)) + "$" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 32)) + "\n")
}

func secretStore(t *testing.T, options StoreOptions) (*Store, *Document) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte(backupSample), 0600))
	s, err := OpenStore(context.Background(), p, filepath.Join(filepath.Dir(p), "state"), options)
	require.NoError(t, err)
	d, err := Parse([]byte(backupSample))
	require.NoError(t, err)
	return s, d
}

func writeRootSecret(t *testing.T, s *Store, data []byte) string {
	t.Helper()
	dir := s.ResolvePath(s.Snapshot().Config().Paths.SecretsDir)
	require.NoError(t, os.MkdirAll(dir, 0700))
	p := filepath.Join(dir, AdminSecretName)
	require.NoError(t, os.WriteFile(p, data, 0600))
	return p
}

func TestSecretBackupRestorePublishesOneGeneration(t *testing.T) {
	s, d := secretStore(t, StoreOptions{Offline: true})
	old, restored := fixtureSecret(1), fixtureSecret(2)
	rootPath := writeRootSecret(t, s, old)
	original, err := s.Backup()
	require.NoError(t, err)
	bundle, err := ReadBackupContents(original)
	require.NoError(t, err)
	assert.Equal(t, d.Bytes(), bundle.Document.Bytes())
	assert.Equal(t, old, bundle.Secrets[AdminSecretName])
	bundle.Secrets[AdminSecretName][0] = 'X'
	again, err := ReadBackupContents(original)
	require.NoError(t, err)
	assert.Equal(t, old, again.Secrets[AdminSecretName])
	archive, err := BackupWithSecrets(d, map[string][]byte{AdminSecretName: restored})
	require.NoError(t, err)
	result, err := s.Restore(context.Background(), d.Revision(), archive)
	require.NoError(t, err)
	id := s.Snapshot().Config().Admin.SecretGeneration
	assert.Len(t, id, 64)
	assert.Equal(t, result.SavedRevision, result.ActiveRevision)
	saved, err := readConfig(s.path)
	require.NoError(t, err)
	assert.Contains(t, string(saved), "# owned by operator")
	assert.Contains(t, string(saved), "listen: '127.0.0.1:0' # private")
	want, err := d.Upsert([]Edit{{Path: []string{"admin", "secret_generation"}, Value: id}})
	require.NoError(t, err)
	assert.Equal(t, want.Bytes(), saved)
	secret, err := s.ActiveSecret(AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, restored, secret)
	legacy, err := os.ReadFile(rootPath)
	require.NoError(t, err)
	assert.Equal(t, old, legacy)
	for _, rel := range []string{"generations", "generations/" + id, "generations/" + id + "/" + AdminSecretName} {
		info, err := os.Stat(filepath.Join(filepath.Dir(rootPath), rel))
		require.NoError(t, err)
		mode := os.FileMode(0700)
		if !info.IsDir() {
			mode = 0600
		}
		assert.Equal(t, mode, info.Mode().Perm())
	}
	// Backing up a restored generation preserves exact text and credential bytes.
	next, err := s.Backup()
	require.NoError(t, err)
	bundle, err = ReadBackupContents(next)
	require.NoError(t, err)
	assert.Equal(t, saved, bundle.Document.Bytes())
	assert.Equal(t, restored, bundle.Secrets[AdminSecretName])
	_, err = Backup(bundle.Document)
	assert.ErrorContains(t, err, "requires admin.hash")
	other, baseline := secretStore(t, StoreOptions{Offline: true})
	_, err = other.Restore(context.Background(), baseline.Revision(), next)
	require.NoError(t, err)
	assert.NotEqual(t, id, other.Snapshot().Config().Admin.SecretGeneration)
	secret, err = other.ActiveSecret(AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, restored, secret)
	// A configuration-only backup cannot silently revert to legacy credentials.
	onlyConfig, err := Backup(d)
	require.NoError(t, err)
	_, err = s.Restore(context.Background(), result.SavedRevision, onlyConfig)
	require.NoError(t, err)
	assert.Equal(t, id, s.Snapshot().Config().Admin.SecretGeneration)
}

type backupRoundTripper func(*http.Request) (*http.Response, error)

func (f backupRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSecretRestoreCancellationCleansOnlyUnpublishedGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fetcher := lists.NewFetcher(&http.Client{Transport: backupRoundTripper(func(*http.Request) (*http.Response, error) {
		cancel() // Called after Restore has staged the private generation.
		return nil, context.Canceled
	})})
	s, d := secretStore(t, StoreOptions{Fetcher: fetcher})
	writeRootSecret(t, s, fixtureSecret(1))
	candidate, err := d.Append([]string{"lists"}, lists.Subscription{ID: "cancel", URL: "https://example.invalid/cancel", Dialect: lists.Domains, DomainKind: policy.Exact, Enabled: true})
	require.NoError(t, err)
	archive, err := BackupWithSecrets(candidate, map[string][]byte{AdminSecretName: fixtureSecret(2)})
	require.NoError(t, err)
	_, err = s.Restore(ctx, d.Revision(), archive)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, d.Revision(), s.Snapshot().Revision())
	saved, err := readConfig(s.path)
	require.NoError(t, err)
	assert.Equal(t, d.Bytes(), saved)
	entries, err := os.ReadDir(filepath.Join(s.ResolvePath(d.Config().Paths.SecretsDir), "generations"))
	require.NoError(t, err)
	assert.Empty(t, entries)
	secret, err := s.ActiveSecret(AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, fixtureSecret(1), secret)
}

func TestSecretRestoreLateSaveFailureRetainsPublishedGeneration(t *testing.T) {
	s, d := secretStore(t, StoreOptions{Offline: true})
	writeRootSecret(t, s, fixtureSecret(1))
	archive, err := BackupWithSecrets(d, map[string][]byte{AdminSecretName: fixtureSecret(2)})
	require.NoError(t, err)
	// Force the recovery rename to fail AFTER Publish has replaced config.yaml.
	artifact := filepath.Join(s.state, "active.manifest")
	require.NoError(t, os.Remove(artifact))
	require.NoError(t, os.Mkdir(artifact, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(artifact, "obstacle"), []byte("x"), 0600))
	result, err := s.Restore(context.Background(), d.Revision(), archive)
	require.Error(t, err)
	assert.Equal(t, d.Revision(), result.ActiveRevision)
	assert.NotEqual(t, result.SavedRevision, result.ActiveRevision)
	secret, err := s.ActiveSecret(AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, fixtureSecret(1), secret)
	saved, err := readConfig(s.path)
	require.NoError(t, err)
	pending, err := Parse(saved)
	require.NoError(t, err)
	require.NotEmpty(t, pending.Config().Admin.SecretGeneration)
	secret, err = s.documentSecret(pending, AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, fixtureSecret(2), secret)
	// Once the obstacle is repaired, the saved document remains activatable.
	require.NoError(t, os.RemoveAll(artifact))
	_, err = s.Reload(context.Background())
	require.NoError(t, err)
	secret, err = s.ActiveSecret(AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, fixtureSecret(2), secret)
}

func TestSecretRestorePreflightHasNoFilesystemEffects(t *testing.T) {
	for _, mode := range []string{"conflict", "paths", "flow", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, d := secretStore(t, StoreOptions{Offline: true})
			candidate := d
			expected := d.Revision()
			ctx := context.Background()
			switch mode {
			case "conflict":
				expected = "outdated"
			case "paths":
				var err error
				candidate, err = d.Upsert([]Edit{{Path: []string{"paths", "secrets_dir"}, Value: filepath.Join(t.TempDir(), "unexpected")}})
				require.NoError(t, err)
			case "flow":
				var err error
				candidate, err = Parse(bytes.Replace(d.Bytes(), []byte("admin:\n  listen: '127.0.0.1:0' # private"), []byte("admin: {listen: '127.0.0.1:0'}"), 1))
				require.NoError(t, err)
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			archive, err := BackupWithSecrets(candidate, map[string][]byte{AdminSecretName: fixtureSecret(2)})
			require.NoError(t, err)
			_, err = s.Restore(ctx, expected, archive)
			require.Error(t, err)
			_, err = os.Lstat(s.ResolvePath(d.Config().Paths.SecretsDir))
			assert.True(t, os.IsNotExist(err))
			if mode == "paths" {
				_, err = os.Lstat(candidate.Config().Paths.SecretsDir)
				assert.True(t, os.IsNotExist(err))
			}
			assert.Equal(t, d.Revision(), s.Snapshot().Revision())
		})
	}
}

func TestSecretSourceRestrictionsAndNoGenerationFallback(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "permissions", "oversized", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := secretStore(t, StoreOptions{Offline: true})
			path := writeRootSecret(t, s, fixtureSecret(1))
			switch kind {
			case "symlink":
				require.NoError(t, os.Rename(path, path+".real"))
				require.NoError(t, os.Symlink(path+".real", path))
			case "directory":
				require.NoError(t, os.Remove(path))
				require.NoError(t, os.Mkdir(path, 0700))
			case "permissions":
				require.NoError(t, os.Chmod(path, 0644))
			case "oversized":
				require.NoError(t, os.WriteFile(path, make([]byte, MaxSecretBytes+1), 0600))
			case "malformed":
				require.NoError(t, os.WriteFile(path, []byte("not-a-hash"), 0600))
			}
			_, err := s.Backup()
			assert.Error(t, err)
			_, err = s.ActiveSecret(AdminSecretName)
			assert.Error(t, err)
		})
	}
	s, d := secretStore(t, StoreOptions{Offline: true})
	_, err := s.Backup()
	require.NoError(t, err) // Missing legacy secret is allowed.
	writeRootSecret(t, s, fixtureSecret(1))
	d, err = d.Upsert([]Edit{{Path: []string{"admin", "secret_generation"}, Value: "deadbeef"}})
	require.NoError(t, err)
	_, err = s.documentSecret(d, AdminSecretName)
	assert.True(t, os.IsNotExist(err))
	before := s.Inspect()
	_, err = s.Save(context.Background(), before.SavedRevision, d)
	require.Error(t, err)
	assert.Equal(t, before.ActiveRevision, s.Snapshot().Revision())
	assert.Equal(t, before.SavedRevision, s.Inspect().SavedRevision)
	require.NoError(t, os.WriteFile(s.path, d.Bytes(), 0600))
	_, err = s.Backup()
	assert.Error(t, err) // Never fall back to the root hash.
}

func TestSecretStagingRejectsSymlinkAndNonprivateDirectories(t *testing.T) {
	for _, where := range []string{"root", "generations", "permissions"} {
		t.Run(where, func(t *testing.T) {
			s, d := secretStore(t, StoreOptions{Offline: true})
			root := s.ResolvePath(d.Config().Paths.SecretsDir)
			outside := t.TempDir()
			if where == "root" {
				require.NoError(t, os.Symlink(outside, root))
			} else {
				require.NoError(t, os.Mkdir(root, 0700))
				if where == "generations" {
					require.NoError(t, os.Symlink(outside, filepath.Join(root, "generations")))
				} else {
					require.NoError(t, os.Chmod(root, 0755))
				}
			}
			archive, err := BackupWithSecrets(d, map[string][]byte{AdminSecretName: fixtureSecret(1)})
			require.NoError(t, err)
			_, err = s.Restore(context.Background(), d.Revision(), archive)
			require.Error(t, err)
			entries, err := os.ReadDir(outside)
			require.NoError(t, err)
			assert.Empty(t, entries)
			assert.Equal(t, d.Revision(), s.Snapshot().Revision())
		})
	}
}

func TestSecretArchiveValidationAndV1Compatibility(t *testing.T) {
	d, err := Parse([]byte(backupSample))
	require.NoError(t, err)
	v1, err := encodeBackup(d, backupManifest{Version: 1, ConfigSHA256: d.Revision(), NecessarySecrets: []string{}}, nil)
	require.NoError(t, err)
	old, err := ReadBackupContents(v1)
	require.NoError(t, err)
	assert.Equal(t, d.Bytes(), old.Document.Bytes())
	assert.Empty(t, old.Secrets)
	for _, name := range []string{"../admin.hash", "/admin.hash", "other.hash", "generations/deadbeef/admin.hash"} {
		_, err := BackupWithSecrets(d, map[string][]byte{name: fixtureSecret(1)})
		assert.Error(t, err)
		m := backupManifest{Version: BackupVersion, ConfigSHA256: d.Revision(), NecessarySecrets: []string{name}, SecretSHA256: map[string]string{name: revision(fixtureSecret(1))}}
		b, err := encodeBackup(d, m, map[string][]byte{name: fixtureSecret(1)})
		require.NoError(t, err)
		_, err = ReadBackupContents(b)
		assert.Error(t, err)
	}
	b, err := BackupWithSecrets(d, map[string][]byte{AdminSecretName: fixtureSecret(1)})
	require.NoError(t, err)
	tampered := bytes.Replace(b, fixtureSecret(1), fixtureSecret(2), 1)
	_, err = ReadBackupContents(tampered)
	assert.ErrorContains(t, err, "checksum")
	for _, id := range []string{"../outside", "/absolute", "a/b", "a\\b", "ABCDEF", strings.Repeat("a", 65)} {
		_, err := d.Upsert([]Edit{{Path: []string{"admin", "secret_generation"}, Value: id}})
		assert.Error(t, err, fmt.Sprintf("generation %q", id))
	}
}

func TestBackupRestoreSourceAndCoordinator(t *testing.T) {
	p, state := fixtureStore(t)
	s, err := OpenStore(context.Background(), p, state, StoreOptions{Offline: true})
	require.NoError(t, err)
	original, err := s.Backup()
	require.NoError(t, err)
	d, err := ReadBackup(original)
	require.NoError(t, err)
	assert.Equal(t, []byte(storeFixture), d.Bytes())
	old := d.Revision()
	result, err := s.Save(context.Background(), old, denyDoc(t))
	require.NoError(t, err)
	_, err = s.Restore(context.Background(), old, original)
	assert.ErrorIs(t, err, ErrConflict)
	assert.Equal(t, result.ActiveRevision, s.Snapshot().Revision())
	restored, err := s.Restore(context.Background(), result.SavedRevision, original)
	require.NoError(t, err)
	assert.Equal(t, old, restored.ActiveRevision)
	assert.Greater(t, restored.ActiveGeneration, result.ActiveGeneration)
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.Equal(t, []byte(storeFixture), b)
	// Both directions own their memory.
	text := d.Bytes()
	text[0] ^= 1
	original[0] ^= 1
	assert.Equal(t, []byte(storeFixture), d.Bytes())
}

func TestBackupPreservesCommentsAndRejectsTampering(t *testing.T) {
	d, err := Parse([]byte(backupSample))
	require.NoError(t, err)
	b, err := Backup(d)
	require.NoError(t, err)
	restored, err := ReadBackup(b)
	require.NoError(t, err)
	assert.Equal(t, []byte(backupSample), restored.Bytes())
	_, err = Backup(nil)
	assert.Error(t, err)
	bad := bytes.Replace(b, []byte("owned by operator"), []byte("owned by intruder"), 1)
	_, err = ReadBackup(bad)
	assert.ErrorContains(t, err, "checksum")
	for _, bad := range [][]byte{b[:len(b)-512], append(bytes.Clone(b), make([]byte, 512)...), append(bytes.Clone(b), b...), make([]byte, MaxBackupBytes+512)} {
		_, err := ReadBackup(bad)
		assert.Error(t, err)
	}
}

func TestBackupRejectsArchiveEntriesBeforePublication(t *testing.T) {
	p, state := fixtureStore(t)
	s, err := OpenStore(context.Background(), p, state, StoreOptions{Offline: true})
	require.NoError(t, err)
	before := s.Inspect()
	for _, tc := range []struct {
		name      string
		typ       byte
		mode      int64
		duplicate bool
	}{
		{"../config.yaml", tar.TypeReg, 0600, false},
		{"/config.yaml", tar.TypeReg, 0600, false},
		{"config.yaml", tar.TypeSymlink, 0600, false},
		{"config.yaml", tar.TypeLink, 0600, false},
		{"config.yaml", tar.TypeReg, 0644, false},
		{"config.yaml", tar.TypeReg, 0600, true},
		{"secrets/token", tar.TypeReg, 0600, false},
		{"history.db", tar.TypeReg, 0600, false},
	} {
		t.Run(tc.name+string(tc.typ), func(t *testing.T) {
			var b bytes.Buffer
			w := tar.NewWriter(&b)
			h := &tar.Header{Name: tc.name, Typeflag: tc.typ, Mode: tc.mode, Format: tar.FormatUSTAR}
			if tc.typ == tar.TypeReg {
				h.Size = int64(len(storeFixture))
			} else {
				h.Linkname = "target"
			}
			n := 1
			if tc.duplicate {
				n = 2
			}
			for i := 0; i < n; i++ {
				require.NoError(t, w.WriteHeader(h))
				if tc.typ == tar.TypeReg {
					_, err := w.Write([]byte(storeFixture))
					require.NoError(t, err)
				}
			}
			require.NoError(t, w.Close())
			_, err := s.Restore(context.Background(), before.SavedRevision, b.Bytes())
			require.Error(t, err)
			assert.Equal(t, before.ActiveGeneration, s.Inspect().ActiveGeneration)
			text, err := os.ReadFile(p)
			require.NoError(t, err)
			assert.Equal(t, []byte(storeFixture), text)
		})
	}
}

func TestBackupRejectsManifestAmbiguityAndOversizedEntry(t *testing.T) {
	d, err := Parse([]byte(backupSample))
	require.NoError(t, err)
	valid, err := Backup(d)
	require.NoError(t, err)
	for _, replacement := range []string{
		`{"version":3,"config_sha256":"` + d.Revision() + `","necessary_secrets":[]}`,
		`{"version":1,"version":1,"config_sha256":"` + d.Revision() + `","necessary_secrets":[]}`,
		`{"version":1,"config_sha256":"` + d.Revision() + `","necessary_secrets":["token"]}`,
	} {
		var b bytes.Buffer
		w := tar.NewWriter(&b)
		for _, e := range []struct {
			name string
			data []byte
		}{{"manifest.json", []byte(replacement)}, {"config.yaml", d.Bytes()}} {
			require.NoError(t, w.WriteHeader(&tar.Header{Name: e.name, Mode: 0600, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR, Size: int64(len(e.data))}))
			_, err := w.Write(e.data)
			require.NoError(t, err)
		}
		require.NoError(t, w.Close())
		_, err := ReadBackup(b.Bytes())
		assert.Error(t, err)
	}
	_, err = ReadBackup(valid)
	require.NoError(t, err)
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	require.NoError(t, w.WriteHeader(&tar.Header{Name: "config.yaml", Mode: 0600, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR, Size: maxConfigBytes + 1}))
	_, err = w.Write(make([]byte, maxConfigBytes+1))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	_, err = ReadBackup(b.Bytes())
	assert.Error(t, err)
}
