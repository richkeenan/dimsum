package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (s *Server) stream(w http.ResponseWriter, r *http.Request, local bool) {
	if s.events.Add(1) > 8 {
		s.events.Add(-1)
		s.fail(w, r, 429, "capacity", "at most eight event connections")
		return
	}
	defer s.events.Add(-1)
	if _, ok := w.(http.Flusher); !ok {
		s.fail(w, r, 503, "unavailable", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	defer func() { _ = rc.SetWriteDeadline(time.Time{}) }()
	send := func(event string, v any) bool {
		b, e := json.Marshal(v)
		if e != nil {
			return false
		}
		if e = rc.SetWriteDeadline(time.Now().Add(2 * time.Second)); e != nil {
			return false
		}
		if _, e = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); e != nil {
			return false
		}
		return rc.Flush() == nil
	}
	// No retained history: reconnect always explicitly instructs a fresh fetch.
	if !send("reset", map[string]any{"fetch_summary": true}) {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !local {
				if _, ok := s.session(r); !ok {
					return
				}
			}
			status, e := s.service.Status()
			if e != nil {
				if !send("unavailable", map[string]string{"message": e.Error()}) {
					return
				}
			} else if !send("status", map[string]any{"configuration": status, "jobs": s.service.Jobs()}) {
				return
			}
		}
	}
}
