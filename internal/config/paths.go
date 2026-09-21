package config

import "path/filepath"

// ConfigPath is the authoritative file used by this coordinator. Relative
// configured filesystem paths are resolved against its containing directory.
func (s *Store) ConfigPath() string { return s.path }

func (s *Store) ResolvePath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(filepath.Dir(s.path), path)
}
