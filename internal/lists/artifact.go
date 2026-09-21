package lists

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Artifacts contain versioned, checksummed compiler inputs, never user intent.
// Recompilation validates them on recovery; native index layouts are not stable.
const artifactMagic = "DIMSUM01"

func WriteArtifact(path string, payload []byte) error {
	h := sha256.Sum256(payload)
	header := make([]byte, 48)
	copy(header, artifactMagic)
	binary.BigEndian.PutUint64(header[8:16], uint64(len(payload)))
	copy(header[16:], h[:])
	return durableWrite(path, io.MultiReader(bytes.NewReader(header), bytes.NewReader(payload)))
}

func ReadArtifact(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var header [48]byte
	if _, err = io.ReadFull(f, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint64(header[8:16])
	if string(header[:8]) != artifactMagic || max <= 0 || size > uint64(max) {
		return nil, fmt.Errorf("artifact: invalid version or length")
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(size)+1))
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(b)
	if uint64(len(b)) != size || !bytes.Equal(h[:], header[16:]) {
		return nil, fmt.Errorf("artifact: length/checksum mismatch")
	}
	return b, nil
}

func durableWrite(path string, r io.Reader) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".dimsum-artifact-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = io.Copy(f, r); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
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
