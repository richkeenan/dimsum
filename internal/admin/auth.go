package admin

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const passwordIterations = 600000

// HashPassword uses a salted PBKDF2-SHA256 record with a fixed work factor.
func HashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 1024 {
		return "", fmt.Errorf("password must contain 12–1024 bytes")
	}
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	key, e := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	if e != nil {
		return "", e
	}
	return "pbkdf2-sha256$600000$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func verifyPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" || parts[1] != "600000" || len(password) > 1024 {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(parts[2])
	if e != nil || len(salt) != 16 {
		return false
	}
	want, e := base64.RawStdEncoding.DecodeString(parts[3])
	if e != nil || len(want) != 32 {
		return false
	}
	got, e := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	return e == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// BootstrapPassword creates a secret once. It never overwrites an existing hash.
func BootstrapPassword(path, password string) error {
	hash, e := HashPassword(password)
	if e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	if _, e = f.WriteString(hash + "\n"); e != nil {
		return e
	}
	return f.Sync()
}
func LoadPasswordHash(path string) (string, error) {
	info, e := os.Lstat(path)
	if e != nil {
		return "", e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 512 {
		return "", fmt.Errorf("password hash must be a restricted regular file (0600)")
	}
	b, e := os.ReadFile(path)
	return strings.TrimSpace(string(b)), e
}
func token() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

type session struct {
	csrf    string
	expires time.Time
}
type authState struct {
	sync.Mutex
	hash     string
	sessions map[string]session
	window   time.Time
	attempts int
}

// SetPasswordHash installs a newly activated credential (for example after a
// configuration restore) and revokes sessions belonging to the previous one.
func (s *Server) SetPasswordHash(hash string) {
	s.auth.Lock()
	defer s.auth.Unlock()
	if s.auth.hash == hash {
		return
	}
	s.auth.hash = hash
	s.auth.sessions = make(map[string]session)
}

func (s *Server) session(r *http.Request) (session, bool) {
	c, e := r.Cookie("dimsum_session")
	if e != nil {
		return session{}, false
	}
	s.auth.Lock()
	defer s.auth.Unlock()
	v, ok := s.auth.sessions[c.Value]
	if ok && !s.now().Before(v.expires) {
		delete(s.auth.sessions, c.Value)
		ok = false
	}
	return v, ok
}
func (s *Server) cookie(w http.ResponseWriter, value string, expires time.Time, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: "dimsum_session", Value: value, Path: "/", HttpOnly: true, Secure: s.options.SecureCookies, SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: maxAge})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	s.auth.Lock()
	hash := s.auth.hash
	s.auth.Unlock()
	if hash == "" {
		s.fail(w, r, 503, "unavailable", "administrator password is not bootstrapped")
		return
	}
	s.auth.Lock()
	now := s.now()
	if now.Sub(s.auth.window) >= time.Minute {
		s.auth.window = now
		s.auth.attempts = 0
	}
	if s.auth.attempts >= 5 {
		s.auth.Unlock()
		s.fail(w, r, 429, "rate_limited", "login rate limit exceeded")
		return
	}
	s.auth.attempts++
	s.auth.Unlock()
	var body struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !verifyPassword(hash, body.Password) {
		s.fail(w, r, 401, "unauthorized", "invalid credentials")
		return
	}
	id, csrf := token(), token()
	expires := now.Add(s.options.SessionTTL)
	s.auth.Lock()
	if s.auth.hash != hash {
		s.auth.Unlock()
		s.fail(w, r, 401, "unauthorized", "credential changed during login")
		return
	}
	for key, v := range s.auth.sessions {
		if !now.Before(v.expires) {
			delete(s.auth.sessions, key)
		}
	}
	if len(s.auth.sessions) >= 32 {
		s.auth.Unlock()
		s.fail(w, r, 429, "capacity", "session capacity reached")
		return
	}
	s.auth.sessions[id] = session{csrf, expires}
	s.auth.Unlock()
	s.cookie(w, id, expires, int(s.options.SessionTTL.Seconds()))
	writeJSON(w, 200, map[string]any{"csrf_token": csrf, "expires_at": expires})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie("dimsum_session"); e == nil {
		s.auth.Lock()
		delete(s.auth.sessions, c.Value)
		s.auth.Unlock()
	}
	s.cookie(w, "", time.Unix(1, 0), -1)
	writeJSON(w, 200, map[string]bool{"logged_out": true})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	d.UseNumber()
	if e := d.Decode(v); e != nil {
		writeError(w, r, 400, "invalid_request", e.Error())
		return false
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		writeError(w, r, 400, "invalid_request", "expected one JSON object")
		return false
	}
	return true
}
