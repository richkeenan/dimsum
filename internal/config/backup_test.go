package config

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const backupSample = "# owned by operator\nversion: 1 # schema\ndns:\n  listen: [\"127.0.0.1:0\"]\nadmin:\n  listen: '127.0.0.1:0' # private\npaths:\n  data_dir:   './data'\n  secrets_dir: './secrets'\n"

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
		`{"version":2,"config_sha256":"` + d.Revision() + `","necessary_secrets":[]}`,
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
