// Package admin adapts shared control operations to authenticated HTTP and a
// permission-protected Unix socket. Never mount LocalHandler on a TCP listener.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/control"
)

type Options struct {
	PasswordHash   string
	AllowedHosts   []string // Exact host:port values, without scheme.
	SecureCookies  bool     // Set for HTTPS, including a trusted HTTPS reverse proxy.
	SessionTTL     time.Duration
	RequestTimeout time.Duration
	Now            func() time.Time
	DownloadBackup func(context.Context, string) (io.ReadCloser, int64, error)
	Tokens         *TokenStore
	MCP            http.Handler
	OpenAPIJSON    []byte
	OpenAPIYAML    []byte
}
type Server struct {
	service   *control.Service
	options   Options
	auth      authState
	events    atomic.Int32
	admission chan struct{}
}

func New(service *control.Service, o Options) *Server {
	if o.SessionTTL <= 0 {
		o.SessionTTL = 8 * time.Hour
	}
	if o.RequestTimeout <= 0 {
		o.RequestTimeout = 15 * time.Second
	}
	return &Server{service: service, options: o, auth: authState{hash: o.PasswordHash, sessions: make(map[string]session)}, admission: make(chan struct{}, 16)}
}
func (s *Server) now() time.Time {
	if s.options.Now != nil {
		return s.options.Now().UTC()
	}
	return time.Now().UTC()
}
func (s *Server) Handler() http.Handler      { return s.handler(false) }
func (s *Server) LocalHandler() http.Handler { return s.handler(true) }
func (s *Server) HTTPServer(addr string) *http.Server {
	return &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
}

type requestKey struct{}

func (s *Server) handler(local bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), requestKey{}, token()))
		w.Header().Set("X-Request-ID", r.Context().Value(requestKey{}).(string))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		bodyLimit := int64(1 << 20)
		if r.URL.Path == "/api/v1/jobs" {
			bodyLimit = 4 << 20
		}
		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
		if !local {
			valid := false
			for _, host := range s.options.AllowedHosts {
				if strings.EqualFold(r.Host, host) {
					valid = true
				}
			}
			if !valid && s.service != nil {
				valid = s.service.AllowsAdminHost(r.Host)
			}
			if !valid {
				s.fail(w, r, 403, "host_rejected", "unrecognized Host")
				return
			}
			origin := r.Header.Get("Origin")
			if origin != "" {
				u, e := url.Parse(origin)
				scheme := "http"
				if s.options.SecureCookies {
					scheme = "https"
				}
				if e != nil || u.Scheme != scheme || !strings.EqualFold(u.Host, r.Host) || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
					s.fail(w, r, 403, "origin_rejected", "cross-origin request rejected")
					return
				}
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				s.fail(w, r, 403, "origin_rejected", "cross-site request rejected")
				return
			}
		}
		if r.URL.Path == "/health/live" && r.Method == "GET" {
			writeJSON(w, 200, map[string]bool{"live": true})
			return
		}
		if r.URL.Path == "/health/ready" && r.Method == "GET" {
			a, e := s.service.Status()
			ready := e == nil && a.ActiveGeneration != "0"
			code := 200
			if !ready {
				code = 503
			}
			writeJSON(w, code, map[string]bool{"ready": ready})
			return
		}
		bearer := false
		if r.URL.Path == "/mcp" || (!local && len(r.Header.Values("Authorization")) > 0) {
			if !s.bearer(r) {
				s.fail(w, r, 401, "unauthorized", "valid bearer token required")
				return
			}
			bearer = true
		}
		if !local && !(r.URL.Path == "/session" && r.Method == "POST") && !(bearer && (strings.HasPrefix(r.URL.Path, "/api/v1/") || r.URL.Path == "/mcp")) {
			sess, ok := s.session(r)
			if !ok {
				s.fail(w, r, 401, "unauthorized", "login required")
				return
			}
			if r.Method != "GET" && r.Method != "HEAD" {
				if r.Header.Get("Origin") == "" || r.Header.Get("X-CSRF-Token") != sess.csrf {
					s.fail(w, r, 403, "csrf_rejected", "Origin and session CSRF token required")
					return
				}
			}
		}
		if r.URL.Path == "/api/v1/events" && r.Method == "GET" {
			if bearer {
				s.tokenStream(w, r)
			} else {
				s.stream(w, r, local)
			}
			return
		}
		select {
		case s.admission <- struct{}{}:
			defer func() { <-s.admission }()
		default:
			s.fail(w, r, 429, "capacity", "request capacity exhausted")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), s.options.RequestTimeout)
		defer cancel()
		r = r.WithContext(ctx)
		rc := http.NewResponseController(w)
		_ = rc.SetWriteDeadline(time.Now().Add(s.options.RequestTimeout))
		_ = rc.SetReadDeadline(time.Now().Add(s.options.RequestTimeout))
		defer func() { _ = rc.SetWriteDeadline(time.Time{}); _ = rc.SetReadDeadline(time.Time{}) }()
		if r.URL.Path == "/mcp" {
			if s.options.MCP == nil {
				s.fail(w, r, 503, "unavailable", "MCP unavailable")
			} else {
				s.options.MCP.ServeHTTP(w, r)
			}
			return
		}
		if s.tokenRoute(w, r) {
			return
		}
		if r.Method == "GET" && (r.URL.Path == "/api/v1/openapi.json" || r.URL.Path == "/api/v1/openapi.yaml") {
			data, contentType := s.options.OpenAPIJSON, "application/json"
			if r.URL.Path == "/api/v1/openapi.yaml" {
				data, contentType = s.options.OpenAPIYAML, "application/yaml"
			}
			if len(data) == 0 {
				s.fail(w, r, 503, "unavailable", "OpenAPI document unavailable")
				return
			}
			w.Header().Set("Content-Type", contentType)
			_, _ = w.Write(data)
			return
		}
		if r.URL.Path == "/session" {
			switch r.Method {
			case "POST":
				s.login(w, r)
			case "DELETE":
				s.logout(w, r)
			default:
				s.fail(w, r, 405, "method_not_allowed", "unsupported session method")
			}
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/config/backups/") && r.Method == "GET" {
			if s.options.DownloadBackup == nil {
				s.fail(w, r, 503, "unavailable", "backup download unavailable")
				return
			}
			reader, size, err := s.options.DownloadBackup(r.Context(), strings.TrimPrefix(r.URL.Path, "/api/v1/config/backups/"))
			if err != nil {
				s.fail(w, r, 404, "not_found", "backup artifact expired or unavailable")
				return
			}
			defer reader.Close()
			w.Header().Set("Content-Type", "application/x-tar")
			w.Header().Set("Content-Disposition", `attachment; filename="dimsum-config.tar"`)
			w.Header().Set("Content-Length", fmt.Sprint(size))
			_, _ = io.Copy(w, reader)
			return
		}
		s.route(w, r)
	})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	message = control.RedactMessage(message)
	id, _ := r.Context().Value(requestKey{}).(string)
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "request_id": id, "field_errors": []any{}}})
}
func (s *Server) fail(w http.ResponseWriter, r *http.Request, status int, code, message string, fields ...control.FieldError) {
	message = control.RedactMessage(message)
	id, _ := r.Context().Value(requestKey{}).(string)
	detail := map[string]any{"code": code, "message": message, "request_id": id, "field_errors": []any{}}
	if len(fields) > 0 {
		for i := range fields {
			fields[i].Message = control.RedactMessage(fields[i].Message)
		}
		detail["field_errors"] = fields
	}
	if status == 409 || status == 422 {
		if a, e := s.service.Status(); e == nil {
			detail["active_generation"] = a.ActiveGeneration
		}
	}
	writeJSON(w, status, map[string]any{"error": detail})
}
func (s *Server) result(w http.ResponseWriter, r *http.Request, v any, e error) {
	if e == nil {
		writeJSON(w, 200, v)
		return
	}
	code, status := "invalid_configuration", 422
	switch {
	case errors.Is(e, control.ErrLeaseCursor):
		code, status = "lease_cursor_expired", 409
	case errors.Is(e, control.BadRequest):
		code, status = "bad_request", 400
	case errors.Is(e, control.NotFound):
		code, status = "not_found", 404
	case errors.Is(e, config.ErrConflict):
		code, status = "revision_conflict", 409
	case errors.Is(e, control.ErrUnavailable):
		code, status = "unavailable", 503
	case errors.Is(e, control.ErrBusy):
		code, status = "capacity", 429
	case errors.Is(e, context.DeadlineExceeded):
		code, status = "deadline_exceeded", 503
	}
	var field *control.FieldError
	if errors.As(e, &field) {
		s.fail(w, r, status, code, e.Error(), *field)
		return
	}
	s.fail(w, r, status, code, e.Error())
}

// ListenUnix recovers stale sockets under an exclusive process lock and requires
// an owner-only parent directory. Live listeners and non-sockets are preserved.
// Callers own listener shutdown; Close removes the socket created by net.ListenUnix.
func (s *Server) ListenUnix(path string) (net.Listener, error) {
	parent, e := os.Lstat(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	if !parent.IsDir() || parent.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("control socket parent must be owner-only (0700)")
	}
	fd, e := syscall.Open(path+".lock", syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if e != nil {
		return nil, e
	}
	lock := os.NewFile(uintptr(fd), path+".lock")
	success := false
	defer func() {
		if !success {
			lock.Close()
		}
	}()
	if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return nil, fmt.Errorf("control socket already owned: %w", e)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("control socket path is not a socket")
		}
		connection, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			connection.Close()
			return nil, fmt.Errorf("control socket already listening")
		}
		if !errors.Is(err, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("cannot establish stale socket: %w", err)
		}
		if e = os.Remove(path); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	l, e := net.Listen("unix", path)
	if e != nil {
		return nil, e
	}
	if e = os.Chmod(path, 0600); e != nil {
		l.Close()
		return nil, e
	}
	success = true
	return &lockedListener{Listener: l, lock: lock}, nil
}

type lockedListener struct {
	net.Listener
	lock *os.File
	once sync.Once
}

func (l *lockedListener) Close() error {
	err := l.Listener.Close()
	l.once.Do(func() { l.lock.Close() })
	return err
}
func (s *Server) ServeUnix(ctx context.Context, path string) error {
	l, e := s.ListenUnix(path)
	if e != nil {
		return e
	}
	srv := s.HTTPServer("")
	srv.Handler = s.LocalHandler()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = srv.Close()
		case <-done:
		}
	}()
	e = srv.Serve(l)
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
