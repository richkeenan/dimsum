package config

import (
	"context"
	"math/rand/v2"
	"time"
)

// Watch polls content, not an inode: writes and rename-over saves both work on
// native and bind-mounted directories. A tick coalesces saves, not transactions.
// Call once per Store; cancellation waits at most the bounded current fetch.
func (s *Store) Watch(ctx context.Context) {
	ticker := time.NewTicker(s.options.PollInterval)
	defer ticker.Stop()
	nextRefresh := time.Now().Add(s.jitter())
	s.mu.Lock()
	seen := s.status.SavedRevision
	s.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		b, err := readConfig(s.path)
		key := ""
		if err != nil {
			key = err.Error()
		} else {
			key = revision(b)
		}
		due := time.Now().After(nextRefresh)
		if key != seen || due {
			seen = key
			// A coordinator Save has already compiled these bytes.
			if !due {
				s.mu.Lock()
				activated := s.status.ActiveRevision == key && s.status.Error == ""
				s.mu.Unlock()
				if activated {
					continue
				}
			}
			_, _ = s.reload(ctx, !due)
			nextRefresh = time.Now().Add(s.jitter())
		}
	}
}
func (s *Store) jitter() time.Duration {
	return time.Duration(float64(s.options.RefreshInterval) * (0.9 + rand.Float64()*0.2))
}
