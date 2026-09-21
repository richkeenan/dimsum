package config

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const AdminSecretName = "admin.hash"
const MaxSecretBytes = 512

func validSecretGeneration(id string) bool {
	if len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validateSecret(name string, data []byte) error {
	if name != AdminSecretName {
		return fmt.Errorf("secret: name %q is not allowlisted", name)
	}
	if len(data) == 0 || len(data) > MaxSecretBytes {
		return fmt.Errorf("secret: credential exceeds size limit or is empty")
	}
	p := strings.Split(strings.TrimSpace(string(data)), "$")
	if len(p) != 4 || p[0] != "pbkdf2-sha256" || p[1] != "600000" {
		return fmt.Errorf("secret: invalid admin credential format")
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(p[2])
	key, e2 := base64.RawStdEncoding.DecodeString(p[3])
	if e1 != nil || e2 != nil || len(salt) != 16 || len(key) != 32 {
		return fmt.Errorf("secret: invalid admin credential encoding")
	}
	return nil
}

// ActiveSecret returns owned credential bytes from exactly one active snapshot.
// An empty generation uses the legacy root admin.hash; a configured generation
// never falls back to root if its file is missing, malformed or inaccessible.
func (s *Store) ActiveSecret(name string) ([]byte, error) {
	active := s.Snapshot()
	if active == nil {
		return nil, fmt.Errorf("secret: no active configuration")
	}
	return s.documentSecret(active.document, name)
}

func (s *Store) validateDocumentSecrets(d *Document) error {
	if d.Config().Admin.SecretGeneration == "" {
		return nil
	}
	_, err := s.documentSecret(d, AdminSecretName)
	return err
}

func (s *Store) documentSecret(d *Document, name string) ([]byte, error) {
	if name != AdminSecretName {
		return nil, fmt.Errorf("secret: name is not allowlisted")
	}
	c := d.Config()
	r, err := openSecretRoot(s.ResolvePath(c.Paths.SecretsDir), false)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if c.Admin.SecretGeneration != "" {
		g, err := childSecretRoot(r, "generations", false)
		if err != nil {
			return nil, err
		}
		defer g.Close()
		r, err = childSecretRoot(g, c.Admin.SecretGeneration, false)
		if err != nil {
			return nil, err
		}
		defer r.Close()
	}
	info, err := r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode() & ^os.FileMode(0600) != 0 || info.Mode().Perm()&0400 == 0 || info.Size() > MaxSecretBytes {
		return nil, fmt.Errorf("secret: expected restricted regular credential file")
	}
	// O_NOFOLLOW and O_NONBLOCK prevent a last-component symlink/FIFO swap
	// between Lstat and open. Root handles confine all descendant traversal.
	f, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Mode() & ^os.FileMode(0600) != 0 {
		return nil, fmt.Errorf("secret: credential changed while opening")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxSecretBytes+1))
	if err != nil {
		return nil, err
	}
	if err := validateSecret(name, b); err != nil {
		return nil, err
	}
	return b, nil
}

// The configured destination parent is operator-owned. Once opened, all
// generation operations use directory handles, not archive-supplied paths.
func openSecretRoot(path string, create bool) (*os.Root, error) {
	if create {
		if err := mkdirSecretRoot(path); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("secret: expected private directory, not a symlink")
	}
	r, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	opened, err := r.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		r.Close()
		return nil, fmt.Errorf("secret: directory changed while opening")
	}
	return r, nil
}

// Sync every newly created ancestor, not just the final secrets directory:
// otherwise a durable config could reference a generation below an unsynced
// newly created parent after a machine crash.
func mkdirSecretRoot(path string) error {
	err := os.Mkdir(path, 0700)
	if os.IsNotExist(err) && filepath.Dir(path) != path {
		if err := mkdirSecretRoot(filepath.Dir(path)); err != nil {
			return err
		}
		err = os.Mkdir(path, 0700)
	}
	if err != nil && !os.IsExist(err) {
		return err
	}
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

func childSecretRoot(parent *os.Root, name string, create bool) (*os.Root, error) {
	if create {
		if err := parent.Mkdir(name, 0700); err != nil && !os.IsExist(err) {
			return nil, err
		}
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("secret: expected private generation directory, not a symlink")
	}
	r, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := r.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		r.Close()
		return nil, fmt.Errorf("secret: generation changed while opening")
	}
	return r, nil
}

func syncSecretDir(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

type secretStage struct{ generations *os.Root }

func (s *secretStage) close() { _ = s.generations.Close() }

func (s *Store) stageSecrets(target Config, id string, secrets map[string][]byte) (_ *secretStage, err error) {
	if id == "" || !validSecretGeneration(id) {
		return nil, fmt.Errorf("secret: invalid staging generation")
	}
	for name, data := range secrets {
		if err := validateSecret(name, data); err != nil {
			return nil, err
		}
	}
	root, err := openSecretRoot(s.ResolvePath(target.Paths.SecretsDir), true)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	g, err := childSecretRoot(root, "generations", true)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = g.Close()
		}
	}()
	// Exclusive creation: no existing generation is ever modified or removed.
	if err = g.Mkdir(id, 0700); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = g.RemoveAll(id)
			_ = syncSecretDir(g)
		}
	}()
	dir, err := childSecretRoot(g, id, false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	for name, data := range secrets {
		f, e := dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, e
		}
		_, e = f.Write(data)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	for _, r := range []*os.Root{dir, g, root} {
		if err = syncSecretDir(r); err != nil {
			return nil, err
		}
	}
	return &secretStage{generations: g}, nil
}
