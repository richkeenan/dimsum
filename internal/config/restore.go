package config

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
)

// BackupContents owns the exact document and each credential's bytes. Changes
// to these values never modify the archive or any on-disk credentials.
type BackupContents struct {
	Document *Document
	Secrets  map[string][]byte
}

// ReadBackup validates the complete archive without extracting anything. It
// returns an owned document for preview. Use Store.Restore to apply the complete
// bundle, not Store.Save on this document alone. The narrow format rejects paths,
// links, devices, extensions, duplicates and secrets outside the allowlist.
func ReadBackup(archive []byte) (*Document, error) {
	b, err := ReadBackupContents(archive)
	if err != nil {
		return nil, err
	}
	return b.Document, nil
}

// ReadBackupContents validates v1 configuration-only and v2 credential archives
// without extraction. A generation in the source is only an identity: Restore
// always stages a new generation under the target store's existing secrets root.
func ReadBackupContents(archive []byte) (*BackupContents, error) {
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
		if h.Name == "secrets/"+AdminSecretName {
			limit = MaxSecretBytes
		}
		if (h.Name != "config.yaml" && h.Name != "manifest.json" && h.Name != "secrets/"+AdminSecretName) || files[h.Name] != nil || h.Typeflag != tar.TypeReg || h.Linkname != "" || h.Mode != 0600 || h.Format != tar.FormatUSTAR || len(h.PAXRecords) != 0 || h.Size < 0 || h.Size > limit {
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
	if files["manifest.json"] == nil || files["config.yaml"] == nil {
		return nil, fmt.Errorf("backup: missing manifest or configuration")
	}
	var m backupManifest
	if err := json.Unmarshal(files["manifest.json"], &m); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(m)
	if err != nil || !bytes.Equal(canonical, files["manifest.json"]) || (m.Version != 1 && m.Version != BackupVersion) || m.NecessarySecrets == nil || len(m.NecessarySecrets) > 1 {
		return nil, fmt.Errorf("backup: unsupported or noncanonical manifest")
	}
	d, err := Parse(files["config.yaml"])
	if err != nil {
		return nil, err
	}
	if d.Revision() != m.ConfigSHA256 {
		return nil, fmt.Errorf("backup: configuration checksum mismatch")
	}
	secrets := map[string][]byte{}
	if len(files) != 2+len(m.NecessarySecrets) || len(m.SecretSHA256) != len(m.NecessarySecrets) || (m.Version == 1 && len(m.NecessarySecrets) != 0) {
		return nil, fmt.Errorf("backup: secret manifest membership mismatch")
	}
	for _, name := range m.NecessarySecrets {
		data, ok := files["secrets/"+name]
		if !ok || m.SecretSHA256[name] != revision(data) {
			return nil, fmt.Errorf("backup: secret checksum or membership mismatch")
		}
		if err := validateSecret(name, data); err != nil {
			return nil, err
		}
		secrets[name] = data
	}
	if d.Config().Admin.SecretGeneration != "" && len(secrets) == 0 {
		return nil, fmt.Errorf("backup: missing referenced credentials")
	}
	expected, err := encodeBackup(d, m, secrets)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(expected, archive) {
		return nil, fmt.Errorf("backup: noncanonical archive envelope")
	}
	return &BackupContents{Document: d, Secrets: secrets}, nil
}

// Restore uses the authoritative coordinator and its expected saved revision.
// Validation has no filesystem effects, so rejected archives cannot partially
// install configuration or credentials. Activation errors retain Store.Save's
// saved-versus-active diagnostics; callers must display the returned result.
// Optional checks validate deployment constraints before any credential staging
// or configuration publication, while archive parsing remains platform independent.
func (s *Store) Restore(ctx context.Context, expected string, archive []byte, checks ...func(*Document) error) (ActivationResult, error) {
	bundle, err := ReadBackupContents(archive)
	if err != nil {
		return s.Inspect(), err
	}
	for _, check := range checks {
		if err := check(bundle.Document); err != nil {
			return s.Inspect(), err
		}
	}
	return s.restoreContents(ctx, expected, bundle)
}

// SetAdminSecret publishes a new immutable credential generation while retaining
// the active document. Pending external configuration edits are never adopted.
func (s *Store) SetAdminSecret(ctx context.Context, expected string, hash []byte) (ActivationResult, error) {
	if err := validateSecret(AdminSecretName, hash); err != nil {
		return s.Inspect(), err
	}
	active := s.Snapshot()
	if active == nil || active.Revision() != expected {
		return s.Inspect(), ErrConflict
	}
	return s.restoreContents(ctx, expected, &BackupContents{Document: active.document, Secrets: map[string][]byte{AdminSecretName: hash}})
}

func (s *Store) restoreContents(ctx context.Context, expected string, bundle *BackupContents) (ActivationResult, error) {
	var err error
	d := bundle.Document
	target, err := s.restoreTarget(expected, d)
	if err != nil {
		return s.Inspect(), err
	}
	if err := ctx.Err(); err != nil {
		return s.Inspect(), err
	}
	id := target.Admin.SecretGeneration
	if len(bundle.Secrets) > 0 {
		var entropy [32]byte
		if _, err = rand.Read(entropy[:]); err != nil {
			return s.Inspect(), err
		}
		id = fmt.Sprintf("%x", entropy)
	}
	if id != d.Config().Admin.SecretGeneration {
		d, err = d.Upsert([]Edit{{Path: []string{"admin", "secret_generation"}, Value: id}})
		if err != nil {
			return s.Inspect(), err
		}
	}
	var stage *secretStage
	if len(bundle.Secrets) > 0 {
		stage, err = s.stageSecrets(target, id, bundle.Secrets)
		if err != nil {
			return s.Inspect(), err
		}
		defer stage.close()
	}
	result, err := s.Save(ctx, expected, d)
	if err != nil && stage != nil {
		s.discardUnreferenced(stage, id)
	}
	return result, err
}

// Check destination-bound settings before creating any staging directories.
func (s *Store) restoreTarget(expected string, d *Document) (Config, error) {
	s.build.Lock()
	defer s.build.Unlock()
	b, err := readConfig(s.path)
	if err != nil {
		return Config{}, err
	}
	if revision(b) != expected {
		return Config{}, ErrConflict
	}
	active := s.Snapshot()
	if active == nil {
		return Config{}, fmt.Errorf("restore: no active destination")
	}
	a, c := active.Config(), d.Config()
	if c.Paths != a.Paths || c.Admin.Listen != a.Admin.Listen || !reflect.DeepEqual(c.DNS.Listen, a.DNS.Listen) || c.Cache.Bytes != a.Cache.Bytes || c.Cache.Shards != a.Cache.Shards || c.Cache.MaxNegativeTTLSeconds != a.Cache.MaxNegativeTTLSeconds {
		return Config{}, fmt.Errorf("restore: destination paths/listeners/startup cache settings differ; explicit restart required")
	}
	return a, nil
}

func (s *Store) discardUnreferenced(stage *secretStage, id string) {
	s.build.Lock()
	defer s.build.Unlock()
	if active := s.Snapshot(); active != nil && active.Config().Admin.SecretGeneration == id {
		return
	}
	b, err := readConfig(s.path)
	if err != nil {
		return
	} // Unknown saved state: retain, never risk deleting a published credential.
	d, err := Parse(b)
	if err != nil || d.Config().Admin.SecretGeneration == id {
		return
	}
	_ = stage.generations.RemoveAll(id)
	_ = syncSecretDir(stage.generations)
}
