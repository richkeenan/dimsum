package workload_test

import (
	"testing"
	"time"

	"github.com/richkeenan/dimsum/bench/workload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeterministicMixAndSchedule(t *testing.T) {
	s := workload.Spec{Family: "household", Seed: 42, Count: 1000, Rate: 300}
	w, err := workload.New(s)
	require.NoError(t, err)
	other, err := workload.New(s)
	require.NoError(t, err)
	counts := map[uint16]int{}
	for i := range s.Count {
		q := w.At(i)
		assert.Equal(t, other.At(i), q)
		assert.Equal(t, time.Duration(int64(i)*int64(time.Second)/300), q.Offset)
		counts[q.Type]++
	}
	assert.Equal(t, map[uint16]int{1: 670, 28: 140, 65: 170, 12: 10, 64: 10}, counts)
	assert.Equal(t, w.Digest(), other.Digest())
}

func TestHotKeyAndChurn(t *testing.T) {
	for _, family := range []string{"hot-key", "churn"} {
		w, err := workload.New(workload.Spec{Family: family, Seed: 42, Count: 1000, Rate: 1000})
		require.NoError(t, err)
		names := map[string]bool{}
		for i := range w.Spec().Count {
			q := w.At(i)
			names[q.Name] = true
			assert.EqualValues(t, 1, q.Type)
		}
		if family == "hot-key" {
			assert.Len(t, names, 1)
		} else {
			assert.Len(t, names, 1000)
		}
	}
}

func TestLimits(t *testing.T) {
	for _, s := range []workload.Spec{
		{Family: "unknown", Count: 1, Rate: 1},
		{Family: "churn", Count: 0, Rate: 1},
		{Family: "churn", Count: workload.MaxQueries + 1, Rate: 1000},
		{Family: "churn", Count: 1, Rate: 0},
		{Family: "churn", Count: 1, Rate: 1_000_000_001},
		{Family: "churn", Count: 602, Rate: 1},
	} {
		_, err := workload.New(s)
		assert.Error(t, err)
	}
}
