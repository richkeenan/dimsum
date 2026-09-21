package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"runtime"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/richkeenan/dimsum/internal/control"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/upstream"
)

const maxSupportBundleBytes = 256 << 10

func diagnosticInput(input json.RawMessage, value any) error {
	if len(input) > 4096 {
		return fmt.Errorf("%w: diagnostic input exceeds 4 KiB", control.BadRequest)
	}
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("%w: diagnostic input must be an object", control.BadRequest)
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("%w: %v", control.BadRequest, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%w: expected one diagnostic input object", control.BadRequest)
	}
	return nil
}

type upstreamProbeResult struct {
	Endpoint         string    `json:"endpoint"`
	Generation       string    `json:"generation"`
	Name             string    `json:"name"`
	QType            string    `json:"qtype"`
	State            string    `json:"state"`
	Responding       bool      `json:"responding"`
	Healthy          bool      `json:"healthy"`
	RCode            *uint16   `json:"rcode,omitempty"`
	Transport        string    `json:"transport,omitempty"`
	Attempts         string    `json:"attempts"`
	DiagnosticProbes string    `json:"diagnostic_probes"`
	DurationUS       string    `json:"duration_us"`
	TimeoutMS        int       `json:"timeout_ms"`
	Error            string    `json:"error,omitempty"`
	CheckedAt        time.Time `json:"checked_at"`
}

// upstreamProbe runs one fixed root NS question through the production DNS
// validator, using a private client so no live pool/circuit/cache is changed.
func (m *managedRuntime) upstreamProbe(ctx context.Context, input json.RawMessage) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var request struct {
		Endpoint  string `json:"endpoint"`
		TimeoutMS *int   `json:"timeout_ms"`
	}
	if err := diagnosticInput(input, &request); err != nil {
		return nil, err
	}
	endpoint, err := netip.ParseAddrPort(request.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: endpoint must be a configured literal IP:port", control.BadRequest)
	}
	endpoint = netip.AddrPortFrom(endpoint.Addr().Unmap(), endpoint.Port())
	timeoutMS := 1000
	if request.TimeoutMS != nil {
		timeoutMS = *request.TimeoutMS
	}
	if timeoutMS < 50 || timeoutMS > 3000 {
		return nil, fmt.Errorf("%w: timeout_ms must be 50–3000", control.BadRequest)
	}
	if m.store == nil || m.store.Snapshot() == nil {
		return nil, control.Unavailable
	}
	snapshot := m.store.Snapshot()
	options := snapshot.UpstreamOptions()
	configured := false
	for _, pool := range [][]netip.AddrPort{options.Endpoints, options.Fallback} {
		for _, candidate := range pool {
			candidate = netip.AddrPortFrom(candidate.Addr().Unmap(), candidate.Port())
			if endpoint == candidate {
				configured = true
			}
		}
	}
	if !configured {
		return nil, fmt.Errorf("%w: endpoint is not an active configured primary or fallback upstream", control.BadRequest)
	}
	timeout := time.Duration(timeoutMS) * time.Millisecond
	client, err := upstream.New(upstream.Options{Endpoints: []netip.AddrPort{endpoint}, MaxOutstanding: 1, MaxAttempts: 2, Timeout: timeout, AttemptTimeout: timeout})
	if err != nil {
		return nil, err
	}
	defer client.Close()
	run, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// ID is replaced cryptographically by the client. RD=1, root, NS, IN.
	question := []byte{0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 1}
	var parsed dnswire.Message
	if err = dnswire.ParseRequest(question, &parsed); err != nil {
		return nil, err
	}
	output := make([]byte, 65535)
	start := time.Now()
	exchange, exchangeErr := client.Exchange(run, question, output)
	// A fresh private client never performs automatic half-open probes. Reclassify
	// each actual diagnostic attempt once, without admitting a client query event.
	exchange.Probes = exchange.Attempts
	if m.observations != nil {
		m.observations.exchange(exchange, exchangeErr)
	}
	result := upstreamProbeResult{Endpoint: endpoint.String(), Generation: strconv.FormatUint(snapshot.Generation(), 10), Name: ".", QType: "NS", State: "no_valid_response", Attempts: strconv.Itoa(exchange.Attempts), DiagnosticProbes: strconv.Itoa(exchange.Attempts), DurationUS: strconv.FormatInt(time.Since(start).Microseconds(), 10), TimeoutMS: timeoutMS, CheckedAt: time.Now().UTC()}
	if err = ctx.Err(); err != nil {
		result.State = "cancelled"
		result.Error = err.Error()
		return result, err
	}
	if exchangeErr == nil {
		var message dnswire.Message
		if err = dnswire.ScanMessage(output[:exchange.N], &message); err != nil {
			return nil, err
		}
		result.Responding = true
		rcode := message.RCode
		result.RCode = &rcode
		result.Healthy = rcode == 0
		result.State = "responding_error"
		if result.Healthy {
			result.State = "healthy"
		}
		result.Transport = "udp"
		if exchange.TCP {
			result.Transport = "tcp"
		}
	} else {
		result.Error = control.RedactMessage(exchangeErr.Error())
		// The shared engine rejects SERVFAIL/REFUSED as forwarding results but its
		// isolated health record still proves a validated DNS response arrived.
		for _, health := range client.Health() {
			if health.Responses > 0 {
				result.Responding = true
				result.State = "responding_error"
				if health.SERVFAIL > 0 {
					rcode := uint16(2)
					result.RCode = &rcode
				} else if health.REFUSED > 0 {
					rcode := uint16(5)
					result.RCode = &rcode
				}
			}
		}
	}
	// A timeout/error response is a completed diagnostic finding. Parent
	// cancellation above remains a failed job rather than a claimed measurement.
	return result, nil
}

type supportBuild struct {
	GoVersion    string `json:"go_version"`
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Version      string `json:"version"`
	Revision     string `json:"revision,omitempty"`
	Modified     string `json:"modified,omitempty"`
}

func diagnosticBuild() supportBuild {
	result := supportBuild{GoVersion: runtime.Version(), OS: runtime.GOOS, Architecture: runtime.GOARCH, Version: "unknown"}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" {
			result.Version = info.Main.Version
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				result.Revision = setting.Value
			case "vcs.modified":
				result.Modified = setting.Value
			}
		}
	}
	return result
}

func (m *managedRuntime) supportBundle(ctx context.Context, input json.RawMessage) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.control == nil {
		return nil, control.Unavailable
	}
	var request struct {
		IncludeHistory bool   `json:"include_query_history"`
		From           string `json:"from"`
		To             string `json:"to"`
		Limit          *int   `json:"limit"`
	}
	if err := diagnosticInput(input, &request); err != nil {
		return nil, err
	}
	if !request.IncludeHistory && (request.From != "" || request.To != "" || request.Limit != nil) {
		return nil, fmt.Errorf("%w: history range/limit require include_query_history=true", control.BadRequest)
	}
	var history any
	if request.IncludeHistory {
		start, e1 := time.Parse(time.RFC3339Nano, request.From)
		end, e2 := time.Parse(time.RFC3339Nano, request.To)
		_, startOffset := start.Zone()
		_, endOffset := end.Zone()
		if e1 != nil || e2 != nil || startOffset != 0 || endOffset != 0 || !end.After(start) || end.Sub(start) > 24*time.Hour {
			return nil, fmt.Errorf("%w: history requires an explicit positive UTC from/to range of at most 24 hours", control.BadRequest)
		}
		limit := 50
		if request.Limit != nil {
			limit = *request.Limit
		}
		if limit < 1 || limit > 200 {
			return nil, fmt.Errorf("%w: history limit must be 1–200", control.BadRequest)
		}
		var err error
		history, err = m.control.Data(ctx, "queries", url.Values{"from": {request.From}, "to": {request.To}, "limit": {strconv.Itoa(limit)}})
		if err != nil {
			return nil, err
		}
	}
	configuration, err := m.control.Inspect("settings")
	if err != nil {
		return nil, err
	}
	diagnostics, err := m.control.Diagnostics(ctx)
	if err != nil {
		return nil, err
	}
	document := map[string]any{"format": "dimsum-support-v1", "generated_at": time.Now().UTC(), "build": diagnosticBuild(), "configuration": configuration, "diagnostics": diagnostics, "contains_secrets": false, "contains_query_history": request.IncludeHistory, "query_history": history}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	// Inspect already redacts configuration URL fields. Also scrub free-form
	// diagnostic messages, preserving arbitrary-size decimal integers losslessly.
	var redacted any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err = decoder.Decode(&redacted); err != nil {
		return nil, err
	}
	var scrub func(any) any
	scrub = func(value any) any {
		switch v := value.(type) {
		case string:
			return control.RedactMessage(v)
		case map[string]any:
			for key, item := range v {
				v[key] = scrub(item)
			}
		case []any:
			for i, item := range v {
				v[i] = scrub(item)
			}
		}
		return value
	}
	encoded, err = json.Marshal(scrub(redacted))
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxSupportBundleBytes {
		return nil, errors.New("support bundle exceeds 256 KiB; reduce the history limit or inspect configuration separately")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}
