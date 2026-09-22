package app

import (
	"context"
	"net/url"
	"time"

	"github.com/richkeenan/dimsum/internal/storage"
)

type historyLatency struct {
	Count                string  `json:"count"`
	AverageUS            *string `json:"average_us"`
	P50US                *string `json:"p50_us"`
	P95US                *string `json:"p95_us"`
	P99US                *string `json:"p99_us"`
	PercentilesAvailable bool    `json:"percentiles_available"`
}

type historyLatencyPoint struct {
	historyLatency
	Time     time.Time `json:"time"`
	Complete bool      `json:"complete"`
	Gap      bool      `json:"gap"`
}

type historyLatencyOutcome struct {
	historyLatency
	Outcome string `json:"outcome"`
}

type historyLatencyBand struct {
	LowerUS string  `json:"lower_us"`
	UpperUS *string `json:"upper_us"` // Exclusive; null is the open-ended final band.
	Count   string  `json:"count"`
}

type historyPerformance struct {
	Summary           historyLatency          `json:"summary"`
	Outcomes          []historyLatencyOutcome `json:"outcomes"`
	Distribution      []historyLatencyBand    `json:"distribution"`
	Points            []historyLatencyPoint   `json:"points"`
	ResolutionSeconds int64                   `json:"resolution_seconds"`
	Range             historyRange            `json:"range"`
	Complete          bool                    `json:"complete"`
	UpdatedAt         time.Time               `json:"updated_at"`
}

func presentLatency(l storage.Latency) historyLatency {
	r := historyLatency{Count: decimal(l.Count)}
	if l.Count == 0 {
		return r
	}
	average := decimal(l.Duration / l.Count)
	r.AverageUS = &average
	r.PercentilesAvailable = l.Fine.Count() == l.Count
	if r.PercentilesAvailable {
		for _, p := range []struct {
			n   uint64
			out **string
		}{{50, &r.P50US}, {95, &r.P95US}, {99, &r.P99US}} {
			if estimate := l.Fine.Percentile(p.n); estimate != nil {
				value := decimal(uint64(*estimate))
				*p.out = &value
			}
		}
	}
	return r
}

func (h *historyProvider) Performance(ctx context.Context, q url.Values) (any, error) {
	if err := h.available(); err != nil {
		return nil, err
	}
	if err := checkParams(q, "resolution_seconds"); err != nil {
		return nil, err
	}
	window, err := h.window(q)
	if err != nil {
		return nil, err
	}
	count := func(width time.Duration) int64 {
		return int64(window.To.Add(-time.Microsecond).Truncate(width).Sub(window.From.Truncate(width))/width) + 1
	}
	width := time.Minute
	if value, ok := q["resolution_seconds"]; ok {
		switch value[0] {
		case "60":
		case "3600":
			width = time.Hour
		case "86400":
			width = 24 * time.Hour
		default:
			return nil, invalid("resolution_seconds must be 60, 3600, or 86400")
		}
	} else {
		if count(width) > 1500 {
			width = time.Hour
		}
		if count(width) > 1500 {
			width = 24 * time.Hour
		}
	}
	if count(width) > 1500 {
		return nil, invalid("performance allows at most 1500 buckets")
	}
	data, err := h.db.Performance(ctx, window.From, window.To, width)
	if err != nil {
		return nil, historyError(err)
	}
	r := historyPerformance{
		Summary: presentLatency(data.Summary), Range: window, Complete: data.Complete,
		ResolutionSeconds: int64(width / time.Second), UpdatedAt: h.now().UTC(),
		Points: []historyLatencyPoint{}, Outcomes: []historyLatencyOutcome{}, Distribution: []historyLatencyBand{},
	}
	for i, l := range data.Outcomes {
		r.Outcomes = append(r.Outcomes, historyLatencyOutcome{presentLatency(l), outcomeNames[i]})
	}
	for _, p := range data.Points {
		r.Points = append(r.Points, historyLatencyPoint{presentLatency(p.Latency), time.UnixMicro(p.Timestamp).UTC(), p.Complete, !p.Complete && p.Count == 0})
	}
	bounds := [...]uint64{0, 100, 500, 1000, 5000, 10000, 100000, 1000000}
	for i, n := range data.Summary.Histogram {
		band := historyLatencyBand{LowerUS: decimal(bounds[i]), Count: decimal(n)}
		if i+1 < len(bounds) {
			upper := decimal(bounds[i+1])
			band.UpperUS = &upper
		}
		r.Distribution = append(r.Distribution, band)
	}
	return r, nil
}
