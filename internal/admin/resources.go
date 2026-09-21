package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/richkeenan/dimsum/internal/control"
)

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		s.fail(w, r, 404, "not_found", "unknown operation")
		return
	}
	resource := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	if r.Method == "GET" {
		if n := r.URL.Query().Get("limit"); n != "" {
			v, e := strconv.Atoi(n)
			if e != nil || v < 1 || v > 200 {
				s.fail(w, r, 400, "bad_request", "limit must be 1–200")
				return
			}
		}
		var v any
		var e error
		switch resource {
		case "catalog":
			v = s.service.Catalog()
		case "clients":
			v, e = s.service.Clients(r.Context(), r.URL.Query())
		case "settings", "lists", "rules", "records", "upstreams", "blocking":
			v, e = s.service.Inspect(resource)
		case "summary", "queries", "rankings", "timeseries":
			v, e = s.service.Data(r.Context(), resource, r.URL.Query())
		case "diagnostics":
			v, e = s.service.Diagnostics(r.Context())
		case "jobs":
			v = map[string]any{"items": s.service.Jobs()}
		default:
			if strings.HasPrefix(resource, "queries/") {
				q := r.URL.Query()
				q.Set("id", strings.TrimPrefix(resource, "queries/"))
				v, e = s.service.Data(r.Context(), "query", q)
			} else {
				s.fail(w, r, 404, "not_found", "unknown operation")
				return
			}
		}
		s.result(w, r, v, e)
		return
	}
	if resource == "password" && r.Method == "POST" {
		var b struct {
			Password string `json:"password"`
		}
		if !decode(w, r, &b) {
			return
		}
		hash, err := HashPassword(b.Password)
		if err != nil {
			s.fail(w, r, 400, "bad_request", err.Error())
			return
		}
		v, err := s.service.SetPasswordHash(r.Context(), hash)
		s.RefreshPasswordHash()
		s.result(w, r, v, err)
		return
	}
	if resource == "rules/test" && r.Method == "POST" {
		var b struct {
			Name       string `json:"name"`
			QType      string `json:"qtype"`
			Generation string `json:"generation"`
		}
		if !decode(w, r, &b) {
			return
		}
		v, e := s.service.TestRule(b.Name, b.Generation)
		s.result(w, r, v, e)
		return
	}
	if resource == "blocking" && r.Method == "PUT" {
		var b control.BlockingMutation
		if !decode(w, r, &b) {
			return
		}
		v, e := s.service.Blocking(r.Context(), b)
		s.result(w, r, v, e)
		return
	}
	if resource == "jobs" && r.Method == "POST" {
		var b struct {
			Kind  string          `json:"kind"`
			Input json.RawMessage `json:"input"`
		}
		if !decode(w, r, &b) {
			return
		}
		v, e := s.service.StartJob(r.Context(), b.Kind, b.Input)
		if e == nil {
			writeJSON(w, 202, v)
		} else {
			s.result(w, r, nil, e)
		}
		return
	}
	if strings.HasPrefix(resource, "config/transactions/") && strings.HasSuffix(resource, "/commit") && r.Method == "POST" {
		id := strings.TrimSuffix(strings.TrimPrefix(resource, "config/transactions/"), "/commit")
		v, e := s.service.Commit(r.Context(), id)
		s.result(w, r, v, e)
		return
	}
	if resource == "config/transactions" && r.Method == "POST" {
		var b control.Mutation
		if !decode(w, r, &b) {
			return
		}
		v, e := s.service.Stage(b)
		s.result(w, r, v, e)
		return
	}
	switch resource {
	case "settings":
		if r.Method != "PATCH" {
			s.fail(w, r, 405, "method_not_allowed", "settings requires PATCH")
			return
		}
	case "lists", "rules", "records", "clients", "upstreams":
		if r.Method != "PATCH" && r.Method != "POST" && r.Method != "DELETE" {
			s.fail(w, r, 405, "method_not_allowed", "unsupported mutation")
			return
		}
	default:
		s.fail(w, r, 404, "not_found", "unknown operation")
		return
	}
	var b control.Mutation
	if !decode(w, r, &b) {
		return
	}
	v, e := s.service.Mutate(r.Context(), resource, r.Method, b)
	s.result(w, r, v, e)
}
