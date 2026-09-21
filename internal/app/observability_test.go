package app

import (
	"net/netip"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/richkeenan/dimsum/internal/upstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObservationCountersAndOwnedHistory(t *testing.T) {
	o := &observability{collector: stats.New(2)}
	r := &transport.Request{Peer: netip.MustParseAddrPort("192.0.2.1:1234"), TCP: true}
	r.Message.Question.Name.Length = 3
	copy(r.Message.Question.Name.Canonical[:], []byte{1, 'a', 0})
	result := transport.Result{Admitted: true, Outcome: transport.PolicyBlock, Arrival: time.Now(), Generation: 1, RuleNumber: 2, Rule: policy.Rule{ID: "block", Pattern: "example.test"}, ResponsePolicy: true, AliasLength: 3}
	copy(result.Alias[:], []byte{1, 'b', 0})
	o.observe(r, result)
	result.Alias[1] = 'c'
	e := <-o.collector.Events()
	metadata, err := o.enrich([]stats.QueryEvent{e})
	require.NoError(t, err)
	require.Len(t, metadata.Rules, 1)
	assert.Contains(t, metadata.Rules[0].Description, "example.test")
	assert.Equal(t, []byte{1, 'b', 0}, metadata.Aliases[e.Sequence])
	o.exchange(upstream.ExchangeResult{Attempts: 3, Probes: 1, Background: true}, nil)
	o.observe(nil, transport.Result{})
	o.observe(nil, transport.Result{Outcome: transport.AdmissionRejected})
	s := o.collector.Snapshot()
	assert.EqualValues(t, 1, s.Admitted)
	assert.EqualValues(t, 1, s.Rejected)
	assert.EqualValues(t, 1, s.Malformed)
	assert.EqualValues(t, 3, s.Attempts)
	assert.EqualValues(t, 2, s.Retries)
	assert.EqualValues(t, 1, s.HealthProbes)
	assert.EqualValues(t, 1, s.BackgroundRefreshes)
}

func TestObservationDoesNotWaitForMetadataConsumer(t *testing.T) {
	o := &observability{collector: stats.New(1)}
	o.mu.Lock()
	done := make(chan struct{})
	go func() {
		o.observe(nil, transport.Result{Admitted: true, Outcome: transport.PolicyBlock, Generation: 1, RuleNumber: 1, AliasLength: 1})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		o.mu.Unlock()
		t.Fatal("DNS observation waited for metadata consumer")
	}
	o.mu.Unlock()
	assert.EqualValues(t, 1, o.collector.Snapshot().Admitted)
}
