package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/bench/runner"
	"github.com/richkeenan/dimsum/bench/workload"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/resolve"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/richkeenan/dimsum/internal/upstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This correctness gate is deliberately separate from performance benchmarks.
// Its independent DNS oracle and per-query allocations are validation overhead.
func TestLocalResolverManifest(t *testing.T) {
	data, err := os.ReadFile("../manifests/local-v1.json")
	require.NoError(t, err)
	var manifest struct {
		Version  int `json:"generator_version"`
		Fixtures []struct {
			Spec     workload.Spec  `json:"spec"`
			SHA256   string         `json:"sha256"`
			QTypes   map[uint16]int `json:"qtypes"`
			Answers  int            `json:"expected_answers"`
			Upstream int            `json:"expected_upstream"`
		} `json:"fixtures"`
	}
	require.NoError(t, json.Unmarshal(data, &manifest))
	require.Equal(t, 1, manifest.Version)
	require.Len(t, manifest.Fixtures, 3)
	for _, fixture := range manifest.Fixtures {
		t.Run(fixture.Spec.Family, func(t *testing.T) {
			w, err := workload.New(fixture.Spec)
			require.NoError(t, err)
			require.Equal(t, fixture.SHA256, w.Digest())
			qtypes := map[uint16]int{}
			for i := range fixture.Spec.Count {
				qtypes[w.At(i).Type]++
			}
			require.Equal(t, fixture.QTypes, qtypes)
			var exchanges atomic.Int64
			u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
				exchanges.Add(1)
				wire := append([]byte(nil), r.Wire...)
				wire[2], wire[3] = 0x81, 0x80
				return testutil.Response{Wire: wire}
			})
			require.NoError(t, err)
			// Drain the fixture's bounded notification queue while requests run.
			done, drained := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(drained)
				for {
					select {
					case <-u.Requests():
					case <-done:
						return
					}
				}
			}()
			defer func() { assert.NoError(t, u.Close()); close(done); <-drained }()
			client, err := upstream.New(upstream.Options{Endpoints: []netip.AddrPort{netip.MustParseAddrPort(u.Address())}, Timeout: time.Second})
			require.NoError(t, err)
			defer func() { assert.NoError(t, client.Close()) }()
			pipeline := resolve.New(client)
			factory := func() runner.Handler {
				out := make([]byte, 65535)
				return func(ctx context.Context, q workload.Query) error {
					request := new(dns.Msg)
					request.SetQuestion(q.Name, q.Type)
					request.Id = uint16(q.Index)
					wire, err := request.Pack()
					if err != nil {
						return err
					}
					r := transport.Request{Wire: wire}
					if err := dnswire.ParseRequest(wire, &r.Message); err != nil {
						return err
					}
					n, err := pipeline.Resolve(ctx, &r, out)
					if err != nil {
						return err
					}
					var reply dns.Msg
					if err := reply.Unpack(out[:n]); err != nil {
						return err
					}
					if reply.Id != request.Id || !reply.Response || reply.Rcode != dns.RcodeSuccess || !reflect.DeepEqual(reply.Question, request.Question) || len(reply.Answer)+len(reply.Ns)+len(reply.Extra) != 0 {
						return fmt.Errorf("incorrect fixture reply at query %d", q.Index)
					}
					return nil
				}
			}
			// Sequential preflight fixes upstream expectations independently of
			// scheduling or concurrent hot-key coalescing.
			handle := factory()
			answers := 0
			for i := range fixture.Spec.Count {
				require.NoError(t, handle(context.Background(), w.At(i)))
				answers++
			}
			require.Equal(t, fixture.Answers, answers)
			require.EqualValues(t, fixture.Upstream, exchanges.Load())
			if fixture.Spec.Family == "churn" {
				// Unique keys keep upstream count deterministic under concurrency.
				exchanges.Store(0)
				r, err := runner.Run(context.Background(), w, runner.Options{Workers: 2, Queue: 100, Timeout: time.Second}, factory)
				require.NoError(t, err)
				assert.Equal(t, fixture.Spec.Count, r.Offered)
				assert.Equal(t, fixture.Answers, r.Counts[runner.Answered])
				assert.EqualValues(t, fixture.Upstream, exchanges.Load())
			}
		})
	}
}
