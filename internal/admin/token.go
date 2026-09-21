package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const maxTokens = 32

var (
	errTokenName     = errors.New("token name must contain 1 to 80 characters")
	errTokenLimit    = errors.New("at most 32 API tokens are allowed")
	errTokenNotFound = errors.New("token not found")
)

// TokenInfo is public metadata; it never contains authentication material.
type TokenInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// CreatedToken includes the secret exactly once, when created.
type CreatedToken struct {
	TokenInfo
	Token string `json:"token"`
}

type tokenRecord struct {
	TokenInfo
	Hash string `json:"hash"`
}

type tokenFile struct {
	Version int           `json:"version"`
	Items   []tokenRecord `json:"items"`
}

// TokenStore serializes mutations and publishes them only after atomic persistence.
// Open one store per process/path. An empty path creates a memory-only store.
type TokenStore struct {
	mu    sync.RWMutex
	path  string
	items []tokenRecord
}

// OpenTokenStore opens an owner-private token file, failing closed on corruption
// or unsafe permissions. A missing file is initialized atomically.
func OpenTokenStore(path string) (*TokenStore, error) {
	s := &TokenStore{path: path, items: []tokenRecord{}}
	if path == "" {
		return s, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if err := tokenParent(path); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		if err = s.persist(s.items); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err = privateTokenFile(info); err != nil {
		return nil, err
	}
	if info.Size() > 64<<10 {
		return nil, errors.New("token store exceeds size limit")
	}
	dec := json.NewDecoder(io.LimitReader(f, 64<<10))
	dec.DisallowUnknownFields()
	var data tokenFile
	if err = dec.Decode(&data); err != nil {
		return nil, fmt.Errorf("invalid token store: %w", err)
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("invalid trailing token store data")
	}
	if data.Version != 1 || data.Items == nil || len(data.Items) > maxTokens {
		return nil, errors.New("invalid token store format")
	}
	ids, hashes := map[string]bool{}, map[string]bool{}
	for _, item := range data.Items {
		id, idErr := hex.DecodeString(item.ID)
		hash, hashErr := hex.DecodeString(item.Hash)
		if idErr != nil || len(id) != 16 || hashErr != nil || len(hash) != sha256.Size || item.Hash != strings.ToLower(item.Hash) || !validTokenName(item.Name) || item.CreatedAt.IsZero() || ids[item.ID] || hashes[item.Hash] {
			return nil, errors.New("invalid token store record")
		}
		ids[item.ID], hashes[item.Hash] = true, true
	}
	s.items = data.Items
	return s, nil
}

func privateTokenFile(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return errors.New("token file must be an owner-owned regular file with mode 0600 and one link")
	}
	return nil
}

func tokenParent(path string) error {
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("token directory must be owner-owned and private (0700)")
	}
	// Reject user-controlled symlink ancestors; root-owned system aliases such as
	// macOS /var are safe to traverse and commonly contain temporary directories.
	for dir = filepath.Dir(dir); dir != "." && dir != "/"; dir = filepath.Dir(dir) {
		info, err = os.Lstat(dir)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			stat, ok = info.Sys().(*syscall.Stat_t)
			if !ok || stat.Uid != 0 {
				return errors.New("token directory has a symlink ancestor")
			}
		}
	}
	return nil
}

func (s *TokenStore) persist(items []tokenRecord) error {
	if s.path == "" {
		return nil
	}
	if err := tokenParent(s.path); err != nil {
		return err
	}
	if info, err := os.Lstat(s.path); err == nil {
		if err = privateTokenFile(info); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.Marshal(tokenFile{Version: 1, Items: items})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".api-tokens-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	// The rename is the commit point. Best-effort directory sync avoids reporting
	// a failed mutation after it has already committed on disk.
	if dir, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func validTokenName(name string) bool {
	return utf8.ValidString(name) && strings.TrimSpace(name) != "" && utf8.RuneCountInString(name) <= 80
}

// List returns public token metadata, including an empty non-nil slice.
func (s *TokenStore) List() []TokenInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]TokenInfo, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, item.TokenInfo)
	}
	return items
}

// Create returns a new 256-bit secret; only its SHA-256 digest is persisted.
func (s *TokenStore) Create(name string) (CreatedToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validTokenName(name) {
		return CreatedToken{}, errTokenName
	}
	if len(s.items) >= maxTokens {
		return CreatedToken{}, errTokenLimit
	}
	var secret [32]byte
	var id [16]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return CreatedToken{}, err
	}
	if _, err := rand.Read(id[:]); err != nil {
		return CreatedToken{}, err
	}
	token := "dimsum_" + base64.RawURLEncoding.EncodeToString(secret[:])
	digest := sha256.Sum256([]byte(token))
	info := TokenInfo{ID: hex.EncodeToString(id[:]), Name: name, CreatedAt: time.Now().UTC()}
	items := append(append([]tokenRecord{}, s.items...), tokenRecord{TokenInfo: info, Hash: hex.EncodeToString(digest[:])})
	if err := s.persist(items); err != nil {
		return CreatedToken{}, err
	}
	s.items = items
	return CreatedToken{TokenInfo: info, Token: token}, nil
}

// Revoke removes a token; subsequent authentication immediately fails.
func (s *TokenStore) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]tokenRecord, 0, len(s.items))
	found := false
	for _, item := range s.items {
		if item.ID == id {
			found = true
		} else {
			items = append(items, item)
		}
	}
	if !found {
		return errTokenNotFound
	}
	if err := s.persist(items); err != nil {
		return err
	}
	s.items = items
	return nil
}

// Authenticate checks a secret against the current token set.
func (s *TokenStore) Authenticate(secret string) bool {
	if s == nil || len(secret) != 50 || !strings.HasPrefix(secret, "dimsum_") {
		return false
	}
	digest := sha256.Sum256([]byte(secret))
	encoded := hex.EncodeToString(digest[:])
	s.mu.RLock()
	defer s.mu.RUnlock()
	found := 0
	for _, item := range s.items {
		found |= subtle.ConstantTimeCompare([]byte(encoded), []byte(item.Hash))
	}
	return found == 1
}
