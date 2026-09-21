package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

func (s *Server) bearer(r *http.Request) bool {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return false
	}
	fields := strings.Fields(values[0])
	return len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") && s.options.Tokens.Authenticate(fields[1])
}

func (s *Server) tokenRoute(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if path != "/api/v1/tokens" && !strings.HasPrefix(path, "/api/v1/tokens/") {
		return false
	}
	if s.options.Tokens == nil {
		s.fail(w, r, 503, "unavailable", "token store unavailable")
		return true
	}
	var err error
	switch {
	case path == "/api/v1/tokens" && r.Method == "GET":
		writeJSON(w, 200, map[string]any{"items": s.options.Tokens.List()})
		return true
	case path == "/api/v1/tokens" && r.Method == "POST":
		var input struct {
			Name string `json:"name"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var extra any
		if err = dec.Decode(&input); err != nil {
			s.fail(w, r, 400, "bad_request", "expected token name JSON")
			return true
		}
		if err = dec.Decode(&extra); err != io.EOF {
			s.fail(w, r, 400, "bad_request", "expected a single JSON object")
			return true
		}
		var created CreatedToken
		created, err = s.options.Tokens.Create(input.Name)
		if err == nil {
			writeJSON(w, 200, created)
			return true
		}
	case strings.HasPrefix(path, "/api/v1/tokens/") && r.Method == "DELETE":
		err = s.options.Tokens.Revoke(strings.TrimPrefix(path, "/api/v1/tokens/"))
		if err == nil {
			writeJSON(w, 200, map[string]bool{"revoked": true})
			return true
		}
	default:
		s.fail(w, r, 405, "method_not_allowed", "unsupported token method")
		return true
	}
	switch {
	case errors.Is(err, errTokenName):
		s.fail(w, r, 400, "bad_request", err.Error())
	case errors.Is(err, errTokenLimit):
		s.fail(w, r, 429, "capacity", err.Error())
	case errors.Is(err, errTokenNotFound):
		s.fail(w, r, 404, "not_found", err.Error())
	default:
		s.fail(w, r, 503, "unavailable", "token store mutation failed")
	}
	return true
}

// Bearer SSE uses the existing streaming bounds, with periodic revocation checks
// in place of browser-session expiry checks.
func (s *Server) tokenStream(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !s.bearer(r) {
					cancel()
					return
				}
			}
		}
	}()
	s.stream(w, r.WithContext(ctx), true)
}
