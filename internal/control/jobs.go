package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"
)

type Job struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	State   string    `json:"state"`
	Created time.Time `json:"created"`
	Result  any       `json:"result,omitempty"`
	Error   string    `json:"error,omitempty"`
}
type jobState struct {
	sync.Mutex
	next    uint64
	running bool
	entries []Job
	closed  bool
	cancel  context.CancelFunc
	done    chan struct{}
}

func (s *Service) Jobs() []Job {
	s.jobs.Lock()
	defer s.jobs.Unlock()
	return append([]Job{}, s.jobs.entries...)
}
func (s *Service) StartJob(ctx context.Context, kind string, input json.RawMessage) (Job, error) {
	if len(input) > 3<<20 {
		return Job{}, fmt.Errorf("job input exceeds 3 MiB")
	}
	input = append(json.RawMessage(nil), input...)
	hook := s.options.Jobs[kind]
	if hook == nil && kind == "refresh" && s.options.Store != nil {
		hook = func(ctx context.Context, _ json.RawMessage) (any, error) {
			a, e := s.options.Store.Reload(ctx)
			return activation(a), e
		}
	}
	if hook == nil {
		return Job{}, fmt.Errorf("%s: %w", kind, ErrUnavailable)
	}
	s.jobs.Lock()
	if s.jobs.closed {
		s.jobs.Unlock()
		return Job{}, ErrUnavailable
	}
	if s.jobs.running {
		s.jobs.Unlock()
		return Job{}, ErrBusy
	}
	s.jobs.running = true
	s.jobs.next++
	j := Job{ID: strconv.FormatUint(s.jobs.next, 10), Kind: kind, State: "running", Created: time.Now().UTC()}
	if len(s.jobs.entries) == 32 {
		s.jobs.entries = s.jobs.entries[1:]
	}
	s.jobs.entries = append(s.jobs.entries, j)
	run, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	done := make(chan struct{})
	s.jobs.cancel = cancel
	s.jobs.done = done
	s.jobs.Unlock()
	go func() {
		defer close(done)
		defer cancel()
		result, e := hook(run, input)
		s.jobs.Lock()
		defer s.jobs.Unlock()
		s.jobs.running = false
		for i := range s.jobs.entries {
			if s.jobs.entries[i].ID == j.ID {
				s.jobs.entries[i].State = "succeeded"
				s.jobs.entries[i].Result = result
				if e != nil {
					s.jobs.entries[i].State = "failed"
					s.jobs.entries[i].Error = RedactMessage(e.Error())
				}
			}
		}
	}()
	return j, nil
}

// Close prevents new jobs and joins the outstanding owner before dependent
// resources are closed. Client cancellation alone does not cancel accepted jobs.
func (s *Service) Close() {
	s.jobs.Lock()
	s.jobs.closed = true
	if s.jobs.cancel != nil {
		s.jobs.cancel()
	}
	done := s.jobs.done
	s.jobs.Unlock()
	if done != nil {
		<-done
	}
}
