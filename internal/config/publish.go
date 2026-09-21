package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var publishMu sync.Mutex

// Publish validates a candidate and atomically replaces the single authoritative
// file. expected is its byte-content SHA-256, or empty for creation. All writers
// in a running service must use its coordinator. This mutex only serializes this
// process: arbitrary editors can still race the final revision check + rename.
// A post-rename directory-sync error means the file may already be published.
func Publish(path, expected string, candidate *Document) error {
	if candidate == nil {
		return fmt.Errorf("publish: nil document")
	}
	if _, err := Parse(candidate.source); err != nil {
		return err
	}
	publishMu.Lock()
	defer publishMu.Unlock()
	check := func() error {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			if expected == "" {
				return nil
			}
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("publish: configuration must be a regular file")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if revision(b) != expected {
			return ErrConflict
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".dimsum-stage-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	// Preserve an existing operator's mode; creation is restricted (0600).
	if info, err := os.Stat(path); err == nil {
		if err = f.Chmod(info.Mode().Perm()); err != nil {
			return err
		}
	}
	if _, err = f.Write(candidate.source); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = check(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
