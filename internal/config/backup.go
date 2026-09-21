package config

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
)

// BackupVersion is independent of the configuration schema version.
const BackupVersion = 1
const MaxBackupBytes = 2 << 20

// Version 1 has no secret entries: the configuration schema has no secret
// references. Never walk secrets_dir (which may contain unrelated credentials).
// Supporting external credentials requires an explicit allowlist and an atomic
// secret-generation publication contract before expanding this format.
type backupManifest struct {
	Version          int      `json:"version"`
	ConfigSHA256     string   `json:"config_sha256"`
	NecessarySecrets []string `json:"necessary_secrets"`
}

// Backup returns an owned, uncompressed tar archive containing the exact source
// text, including comments. Treat the result as private; write it with mode 0600.
// Derived state, downloaded lists, recovery artifacts and history are excluded.
func Backup(d *Document) ([]byte, error) {
	if d == nil {
		return nil, fmt.Errorf("backup: nil document")
	}
	if _, err := Parse(d.Bytes()); err != nil {
		return nil, err
	}
	manifest, err := json.Marshal(backupManifest{BackupVersion, d.Revision(), []string{}})
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	w := tar.NewWriter(&out)
	for _, entry := range []struct {
		name string
		data []byte
	}{{"manifest.json", manifest}, {"config.yaml", d.Bytes()}} {
		if err := w.WriteHeader(&tar.Header{Name: entry.name, Mode: 0600, Size: int64(len(entry.data)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			return nil, err
		}
		if _, err := w.Write(entry.data); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Backup exports validated saved intent, not a potentially older active view.
func (s *Store) Backup() ([]byte, error) {
	s.build.Lock()
	defer s.build.Unlock()
	b, err := readConfig(s.path)
	if err != nil {
		return nil, err
	}
	d, err := Parse(b)
	if err != nil {
		return nil, err
	}
	return Backup(d)
}
