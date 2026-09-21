package config

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// ReadBackup validates the complete archive without extracting anything. It
// returns an owned document suitable for preview or Store.Save. The deliberately
// narrow format rejects paths, links, devices, extensions, duplicates and extra
// files, including secrets not used by this schema.
func ReadBackup(archive []byte) (*Document, error) {
	if len(archive) > MaxBackupBytes || len(archive) < 1024 || len(archive)%512 != 0 {
		return nil, fmt.Errorf("backup: invalid archive size")
	}
	raw := bytes.NewReader(archive)
	r := tar.NewReader(raw)
	files := map[string][]byte{}
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("backup: %w", err)
		}
		limit := int64(maxConfigBytes)
		if h.Name == "manifest.json" {
			limit = 4096
		}
		if (h.Name != "config.yaml" && h.Name != "manifest.json") || files[h.Name] != nil || h.Typeflag != tar.TypeReg || h.Linkname != "" || h.Mode != 0600 || h.Format != tar.FormatUSTAR || len(h.PAXRecords) != 0 || h.Size < 0 || h.Size > limit {
			return nil, fmt.Errorf("backup: forbidden or duplicate entry %q", h.Name)
		}
		b, err := io.ReadAll(io.LimitReader(r, limit+1))
		if err != nil {
			return nil, err
		}
		files[h.Name] = b
	}
	// Require our canonical envelope too: this catches hidden tar metadata,
	// concatenated archives, nonzero padding and missing end markers.
	if len(files) != 2 {
		return nil, fmt.Errorf("backup: missing manifest or configuration")
	}
	var m backupManifest
	if err := json.Unmarshal(files["manifest.json"], &m); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(m)
	if err != nil || !bytes.Equal(canonical, files["manifest.json"]) || m.Version != BackupVersion || m.NecessarySecrets == nil || len(m.NecessarySecrets) != 0 {
		return nil, fmt.Errorf("backup: unsupported or noncanonical manifest")
	}
	d, err := Parse(files["config.yaml"])
	if err != nil {
		return nil, err
	}
	if d.Revision() != m.ConfigSHA256 {
		return nil, fmt.Errorf("backup: configuration checksum mismatch")
	}
	expected, err := Backup(d)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(expected, archive) {
		return nil, fmt.Errorf("backup: noncanonical archive envelope")
	}
	return d, nil
}

// Restore uses the authoritative coordinator and its expected saved revision.
// Validation has no filesystem effects, so rejected archives cannot partially
// install configuration or credentials. Activation errors retain Store.Save's
// saved-versus-active diagnostics; callers must display the returned result.
func (s *Store) Restore(ctx context.Context, expected string, archive []byte) (ActivationResult, error) {
	d, err := ReadBackup(archive)
	if err != nil {
		return s.Inspect(), err
	}
	return s.Save(ctx, expected, d)
}
