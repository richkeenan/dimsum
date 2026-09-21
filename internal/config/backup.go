package config

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// BackupVersion is independent of the configuration schema version.
const BackupVersion = 2
const MaxBackupBytes = 2 << 20

type backupManifest struct {
	Version          int               `json:"version"`
	ConfigSHA256     string            `json:"config_sha256"`
	NecessarySecrets []string          `json:"necessary_secrets"`
	SecretSHA256     map[string]string `json:"secret_sha256,omitempty"`
}

// Backup returns an owned, uncompressed tar archive containing the exact source
// text, including comments. Treat the result as private; write it with mode 0600.
// Derived state, downloaded lists, recovery artifacts and history are excluded.
func Backup(d *Document) ([]byte, error) {
	return BackupWithSecrets(d, nil)
}

// BackupWithSecrets includes only explicitly supplied necessary credentials.
// It never follows configuration paths; the caller owns the supplied bytes.
// Use Store.Backup to collect credentials from the saved document's generation.
func BackupWithSecrets(d *Document, secrets map[string][]byte) ([]byte, error) {
	if d == nil {
		return nil, fmt.Errorf("backup: nil document")
	}
	if _, err := Parse(d.Bytes()); err != nil {
		return nil, err
	}
	m := backupManifest{Version: BackupVersion, ConfigSHA256: d.Revision(), NecessarySecrets: []string{}}
	for name, data := range secrets {
		if err := validateSecret(name, data); err != nil {
			return nil, err
		}
		m.NecessarySecrets = append(m.NecessarySecrets, name)
		m.SecretSHA256 = map[string]string{name: revision(data)}
	}
	if d.Config().Admin.SecretGeneration != "" && len(secrets) == 0 {
		return nil, fmt.Errorf("backup: referenced credential generation requires %s", AdminSecretName)
	}
	return encodeBackup(d, m, secrets)
}

func encodeBackup(d *Document, m backupManifest, secrets map[string][]byte) ([]byte, error) {
	manifest, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	w := tar.NewWriter(&out)
	entries := []struct {
		name string
		data []byte
	}{{"manifest.json", manifest}, {"config.yaml", d.Bytes()}}
	for _, name := range m.NecessarySecrets {
		entries = append(entries, struct {
			name string
			data []byte
		}{"secrets/" + name, secrets[name]})
	}
	for _, entry := range entries {
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
	secrets := map[string][]byte{}
	b, err = s.documentSecret(d, AdminSecretName)
	if err == nil {
		secrets[AdminSecretName] = b
	} else if !os.IsNotExist(err) || d.Config().Admin.SecretGeneration != "" {
		return nil, err
	}
	return BackupWithSecrets(d, secrets)
}
