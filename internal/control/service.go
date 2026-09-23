// Package control owns operations shared by HTTP and the local CLI transport.
package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dhcp"
	"github.com/richkeenan/dimsum/internal/localdns"
	"go.yaml.in/yaml/v3"
)

var ErrUnavailable = errors.New("operation dependency unavailable")
var ErrBusy = errors.New("operation capacity exhausted")

type Provider interface {
	Summary(context.Context, url.Values) (any, error)
	Queries(context.Context, url.Values) (any, error)
	Query(context.Context, url.Values) (any, error)
	Rankings(context.Context, url.Values) (any, error)
	Timeseries(context.Context, url.Values) (any, error)
}

// PerformanceProvider extends retained history without requiring third-party
// providers to synthesize timing distributions they do not collect.
type PerformanceProvider interface {
	Performance(context.Context, url.Values) (any, error)
}
type Options struct {
	Store      *config.Store
	ConfigPath string
	Provider   Provider
	// Hooks must honor cancellation; the service runs at most one job at a time.
	Jobs        map[string]func(context.Context, json.RawMessage) (any, error)
	Diagnostics func(context.Context) (any, error)
	BootID      string
	DHCPStatus  func() any
	DHCPInspect func() dhcp.LeaseSnapshot
	Leases      func(*config.Snapshot) *localdns.Leases
	// A custom packet transport can supply its own deployment capability.
	DHCPAvailability func() dhcp.Availability
}
type Service struct {
	options Options
	jobs    jobState
}

func New(o Options) *Service {
	if o.BootID == "" {
		o.BootID = rand.Text()
	}
	return &Service{options: o}
}

type Activation struct {
	SavedRevision    string                `json:"saved_revision"`
	ActiveRevision   string                `json:"active_revision"`
	ActiveGeneration string                `json:"active_generation"`
	Pending          bool                  `json:"pending"`
	Recovered        bool                  `json:"recovered"`
	RestartRequired  bool                  `json:"restart_required"`
	Error            string                `json:"error,omitempty"`
	Sources          []config.SourceStatus `json:"sources"`
	DashboardHosts   []DashboardHost       `json:"dashboard_hosts,omitempty"`
}

func activation(a config.ActivationResult) Activation {
	a.Error = RedactMessage(a.Error)
	for i := range a.Sources {
		a.Sources[i].Error = RedactMessage(a.Sources[i].Error)
	}
	if a.Sources == nil {
		a.Sources = []config.SourceStatus{}
	}
	return Activation{a.SavedRevision, a.ActiveRevision, strconv.FormatUint(a.ActiveGeneration, 10), a.Pending, a.Recovered, a.RestartRequired, a.Error, a.Sources, nil}
}

var messageURL = regexp.MustCompile(`https?://[^\s"'<>]+`)

// RedactMessage removes URL credentials echoed by HTTP client errors.
func RedactMessage(text string) string {
	return messageURL.ReplaceAllStringFunc(text, func(raw string) string {
		u, e := url.Parse(raw)
		if e != nil {
			return "[redacted URL]"
		}
		q := u.Query()
		for key := range q {
			q.Set(key, "[redacted]")
		}
		u.RawQuery = q.Encode()
		u.Fragment = ""
		if u.User != nil {
			u.User = url.User("[redacted]")
		}
		return u.String()
	})
}
func (s *Service) Status() (Activation, error) {
	if s.options.Store == nil {
		return Activation{}, ErrUnavailable
	}
	return activation(s.options.Store.Inspect()), nil
}
func (s *Service) document() (*config.Document, error) {
	return s.documentAt("")
}
func (s *Service) documentAt(expected string) (*config.Document, error) {
	f, e := os.Open(s.options.ConfigPath)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil {
		return nil, e
	}
	if expected != "" && fmt.Sprintf("%x", sha256.Sum256(b)) != expected {
		return nil, config.ErrConflict
	}
	return config.Parse(b)
}
func shape(v any) (any, error) {
	b, e := yaml.Marshal(v)
	if e != nil {
		return nil, e
	}
	var out any
	e = yaml.Unmarshal(b, &out)
	return out, e
}
func (s *Service) Inspect(resource string) (any, error) {
	status, e := s.Status()
	if e != nil {
		return nil, e
	}
	d, e := s.document()
	if e != nil {
		return map[string]any{"status": status, "configuration_error": RedactMessage(e.Error())}, nil
	}
	c := d.Config()
	var value any
	switch resource {
	case "settings":
		value = c
	case "lists":
		value = c.Lists
	case "rules":
		value = c.Rules
	case "records":
		value = c.Records
	case "clients":
		value = c.Clients
	case "profiles":
		value = c.Profiles
	case "upstreams":
		value = c.DNS.Upstreams
	case "blocking":
		return map[string]any{"status": status, "enabled": !c.Filtering.Paused(time.Now()), "pause_until": c.Filtering.PauseUntil}, nil
	default:
		return nil, ErrUnavailable
	}
	v, e := shape(value)
	if e != nil {
		return nil, e
	}
	redactURLs(v)
	key := "items"
	if resource == "settings" {
		key = "config"
	} else if v == nil {
		v = []any{}
	}
	return map[string]any{"status": status, key: v}, nil
}

// URL query parameters may contain subscription credentials. The authoritative
// file and backup hook retain them; ordinary structured inspection does not.
func redactURLs(v any) {
	switch node := v.(type) {
	case map[string]any:
		for k, value := range node {
			if strings.EqualFold(k, "url") {
				if text, ok := value.(string); ok {
					if u, e := url.Parse(text); e == nil {
						q := u.Query()
						for key := range q {
							q.Set(key, "[redacted]")
						}
						u.RawQuery = q.Encode()
						u.Fragment = ""
						if u.User != nil {
							u.User = url.User("[redacted]")
						}
						node[k] = u.String()
					}
				}
			} else {
				redactURLs(value)
			}
		}
	case []any:
		for _, item := range node {
			redactURLs(item)
		}
	}
}

type Mutation struct {
	Revision        string        `json:"revision"`
	Edits           []config.Edit `json:"edits,omitempty"`
	Item            any           `json:"item,omitempty"`
	Index           *int          `json:"index,omitempty"`
	AcceptAdminHost string        `json:"accept_admin_host,omitempty"`
}

func path(resource string) []string {
	if resource == "upstreams" {
		return []string{"dns", "upstreams"}
	}
	return []string{resource}
}
func normalizeEdits(edits []config.Edit) error {
	for i := range edits {
		if n, ok := edits[i].Value.(json.Number); ok {
			v, e := n.Int64()
			if e != nil {
				return fmt.Errorf("edits require integer numbers: %w", e)
			}
			edits[i].Value = v
		}
	}
	return nil
}

func normalizeItem(v any) (any, error) {
	switch value := v.(type) {
	case json.Number:
		return value.Int64()
	case map[string]any:
		result := make(map[string]any, len(value))
		for k, item := range value {
			n, e := normalizeItem(item)
			if e != nil {
				return nil, e
			}
			result[k] = n
		}
		return result, nil
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			n, e := normalizeItem(item)
			if e != nil {
				return nil, e
			}
			result[i] = n
		}
		return result, nil
	default:
		return v, nil
	}
}
func (s *Service) candidate(resource, method string, m Mutation) (*config.Document, error) {
	if m.Revision == "" {
		return nil, fmt.Errorf("revision is required")
	}
	d, e := s.documentAt(m.Revision)
	if e != nil {
		return nil, e
	}
	if d.Revision() != m.Revision {
		return nil, config.ErrConflict
	}
	if m.AcceptAdminHost != "" {
		if resource != "records" || method != "PATCH" || len(m.Edits) != 0 || m.Item != nil || m.Index != nil {
			return nil, fmt.Errorf("accept_admin_host requires a records PATCH without other edits")
		}
		return s.acceptDashboardHost(d, m.AcceptAdminHost)
	}
	if e = normalizeEdits(m.Edits); e != nil {
		return nil, e
	}
	switch method {
	case "PATCH":
		if len(m.Edits) == 0 {
			return nil, fmt.Errorf("edits are required")
		}
		if resource != "settings" {
			for i := range m.Edits {
				m.Edits[i].Path = append(path(resource), m.Edits[i].Path...)
			}
		}
		if resource == "settings" {
			return d.Upsert(m.Edits)
		}
		return d.Edit(m.Edits)
	case "POST":
		if m.Item == nil {
			return nil, fmt.Errorf("item is required")
		}
		item, e := normalizeItem(m.Item)
		if e != nil {
			return nil, e
		}
		if resource == "upstreams" {
			return appendUpstream(d, item)
		}
		return d.Append(path(resource), item)
	case "DELETE":
		if m.Index == nil || *m.Index < 0 {
			return nil, fmt.Errorf("nonnegative index required")
		}
		return d.Remove(append(path(resource), strconv.Itoa(*m.Index)))
	}
	return nil, fmt.Errorf("unsupported mutation")
}
func (s *Service) Mutate(ctx context.Context, resource, method string, m Mutation) (any, error) {
	if s.options.Store == nil {
		return nil, ErrUnavailable
	}
	d, e := s.candidate(resource, method, m)
	if e != nil {
		return nil, e
	}
	if e = s.checkDHCPAvailability(d); e != nil {
		return nil, e
	}
	a, e := s.options.Store.Save(ctx, m.Revision, d)
	result := activation(a)
	if e == nil && resource == "records" && method != "DELETE" {
		result.DashboardHosts = dashboardHosts(d.Config())
	}
	return result, e
}
func (s *Service) Stage(m Mutation) (any, error) {
	if s.options.Store == nil {
		return nil, ErrUnavailable
	}
	d, e := s.candidate("settings", "PATCH", m)
	if e != nil {
		return nil, e
	}
	if e = s.checkDHCPAvailability(d); e != nil {
		return nil, e
	}
	id, e := s.options.Store.Stage(m.Revision, d)
	return map[string]any{"id": id, "revision": m.Revision}, e
}
func (s *Service) Commit(ctx context.Context, id string) (any, error) {
	if s.options.Store == nil {
		return nil, ErrUnavailable
	}
	a, e := s.options.Store.CommitStage(ctx, id, s.checkDHCPAvailability)
	return activation(a), e
}
func (s *Service) Data(ctx context.Context, resource string, q url.Values) (any, error) {
	p := s.options.Provider
	if p == nil {
		return nil, ErrUnavailable
	}
	switch resource {
	case "summary":
		return p.Summary(ctx, q)
	case "queries":
		return p.Queries(ctx, q)
	case "query":
		return p.Query(ctx, q)
	case "rankings":
		return p.Rankings(ctx, q)
	case "timeseries":
		return p.Timeseries(ctx, q)
	case "performance":
		if performance, ok := p.(PerformanceProvider); ok {
			return performance.Performance(ctx, q)
		}
	}
	return nil, ErrUnavailable
}
func (s *Service) Diagnostics(ctx context.Context) (any, error) {
	if s.options.Diagnostics != nil {
		return s.options.Diagnostics(ctx)
	}
	a, e := s.Status()
	return map[string]any{"configuration": a, "history_available": s.options.Provider != nil}, e
}
func (s *Service) TestRule(name, generation string) (any, error) {
	return s.ExplainClientPolicy(ClientPolicyExplain{Name: name, Generation: generation})
}

type BlockingMutation struct {
	Revision   string    `json:"revision"`
	PauseUntil time.Time `json:"pause_until"`
	Enabled    bool      `json:"enabled"`
}

func (s *Service) Blocking(ctx context.Context, m BlockingMutation) (any, error) {
	if s.options.Store == nil {
		return nil, ErrUnavailable
	}
	if m.Revision == "" {
		return nil, fmt.Errorf("revision is required")
	}
	d, e := s.documentAt(m.Revision)
	if e != nil {
		return nil, e
	}
	if d.Revision() != m.Revision {
		return nil, config.ErrConflict
	}
	until := m.PauseUntil.UTC()
	if m.Enabled {
		until = time.Time{}
	} else if !until.After(time.Now()) {
		return nil, fmt.Errorf("pause_until must be an absolute future timestamp")
	}
	value := until.Format(time.RFC3339Nano)
	candidate, e := d.Edit([]config.Edit{{Path: []string{"filtering", "pause_until"}, Value: value}})
	if e != nil {
		candidate, e = insertMissingField(d, "filtering", "pause_until", value)
		if e != nil {
			return nil, e
		}
	}
	a, e := s.options.Store.Save(ctx, m.Revision, candidate)
	return activation(a), e
}

// Encode only a new field, never an existing section. Insertion directly after
// the section header leaves comments attached to existing children untouched.
func insertMissingField(d *config.Document, section, key string, value any) (*config.Document, error) {
	var root yaml.Node
	if e := yaml.Unmarshal(d.Bytes(), &root); e != nil {
		return nil, e
	}
	var node, header *yaml.Node
	for i := 0; i < len(root.Content[0].Content); i += 2 {
		if root.Content[0].Content[i].Value == section {
			header = root.Content[0].Content[i]
			node = root.Content[0].Content[i+1]
		}
	}
	text := string(d.Bytes())
	if node == nil {
		b, e := yaml.Marshal(map[string]any{section: map[string]any{key: value}})
		if e != nil {
			return nil, e
		}
		return config.Parse([]byte(text + "\n" + string(b)))
	}
	if node.Kind != yaml.MappingNode || node.Style != 0 {
		return nil, fmt.Errorf("%s requires block mapping", section)
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return nil, fmt.Errorf("%s.%s already exists and requires an editable shape", section, key)
		}
	}
	b, e := yaml.Marshal(map[string]any{key: value})
	if e != nil {
		return nil, e
	}
	var insertion strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		insertion.WriteString(strings.Repeat(" ", node.Column-1))
		insertion.WriteString(line)
		insertion.WriteByte('\n')
	}
	lines := strings.SplitAfter(text, "\n")
	at := header.Line
	out := strings.Join(lines[:at], "") + insertion.String() + strings.Join(lines[at:], "")
	return config.Parse([]byte(out))
}
