package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/control"
	"github.com/stretchr/testify/assert"
)

// A blocked socket eventually fails its write deadline. Model that failure
// directly so the test does not depend on the OS send-buffer capacity.
type slowWriter struct {
	header   http.Header
	deadline time.Time
	writes   int
}

func (w *slowWriter) Header() http.Header { return w.header }
func (w *slowWriter) WriteHeader(int)     {}
func (w *slowWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, errors.New("write deadline exceeded")
}
func (w *slowWriter) Flush() {}
func (w *slowWriter) SetWriteDeadline(t time.Time) error {
	if !t.IsZero() {
		w.deadline = t
	}
	return nil
}
func TestSlowStreamDisconnectsWithoutRetainingSlot(t *testing.T) {
	s := New(control.New(control.Options{}), Options{})
	w := &slowWriter{header: make(http.Header)}
	before := time.Now()
	s.stream(w, httptest.NewRequest("GET", "/api/v1/events", nil), true)
	assert.Equal(t, 1, w.writes)
	assert.WithinDuration(t, before.Add(2*time.Second), w.deadline, time.Second)
	assert.Zero(t, s.events.Load())
}
