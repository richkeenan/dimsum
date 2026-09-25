package config

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/dhcp"
	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/localdns"
	"github.com/richkeenan/dimsum/internal/policy"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	DHCP       dhcp.Settings        `yaml:"dhcp,omitempty" json:"dhcp"`
	Version    int                  `yaml:"version"`
	DNS        DNS                  `yaml:"dns"`
	Admin      Admin                `yaml:"admin"`
	Paths      Paths                `yaml:"paths"`
	Cache      Cache                `yaml:"cache"`
	Lists      []lists.Subscription `yaml:"lists,omitempty"`
	Rules      []CustomRule         `yaml:"rules,omitempty"`
	Records    []Record             `yaml:"records,omitempty"`
	Clients    []ClientOverride     `yaml:"clients,omitempty"`
	Profiles   []Profile            `yaml:"profiles,omitempty"`
	Blocking   *bool                `yaml:"blocking,omitempty"`
	Zones      []localdns.Zone      `yaml:"zones,omitempty"`
	Filtering  policy.Settings      `yaml:"filtering,omitempty"`
	Naming     clients.Settings     `yaml:"naming,omitempty"`
	Statistics Statistics           `yaml:"statistics,omitempty" json:"statistics"`
}
type DNS struct {
	Listen         []string         `yaml:"listen"`
	Upstreams      []string         `yaml:"upstreams,omitempty"`
	Fallback       []string         `yaml:"fallback_upstreams,omitempty"`
	BootstrapDNS   []string         `yaml:"bootstrap_dns,omitempty"`
	UpstreamPolicy UpstreamSettings `yaml:"upstream_policy,omitempty"`
}
type Admin struct {
	ControlSocket    string   `yaml:"control_socket,omitempty"`
	AllowedHosts     []string `yaml:"allowed_hosts,omitempty"`
	SecureCookies    bool     `yaml:"secure_cookies,omitempty"`
	Listen           string   `yaml:"listen"`
	SecretGeneration string   `yaml:"secret_generation,omitempty"`
}
type Paths struct {
	DataDir    string `yaml:"data_dir"`
	SecretsDir string `yaml:"secrets_dir"`
}
type Cache struct {
	Bytes                 int    `yaml:"bytes" json:"bytes"`
	Shards                int    `yaml:"shards" json:"shards"`
	MaxNegativeTTLSeconds int    `yaml:"max_negative_ttl_seconds" json:"max_negative_ttl_seconds"`
	StaleTTLSeconds       int    `yaml:"stale_ttl_seconds" json:"stale_ttl_seconds"`
	StaleMode             string `yaml:"stale_mode" json:"stale_mode"`
	MaxStaleSeconds       int    `yaml:"max_stale_seconds" json:"max_stale_seconds"`
}

type Statistics struct {
	DetailDays int `yaml:"detail_days" json:"detail_days"`
	MinuteDays int `yaml:"minute_days" json:"minute_days"`
	HourDays   int `yaml:"hour_days" json:"hour_days"`
	DayDays    int `yaml:"day_days" json:"day_days"`
}

func DefaultStatistics() Statistics {
	return Statistics{DetailDays: 7, MinuteDays: 7, HourDays: 90, DayDays: 365}
}

// Document owns the original bytes. Never serialize Config over the user's file:
// doing so would discard comments and formatting.
type Document struct {
	source []byte
	value  Config
	root   yaml.Node
}
type Edit struct {
	Path  []string
	Value any
}

var ErrConflict = errors.New("configuration revision conflict")

// Default is a safe local development baseline; file parsing requires explicit
// version, listeners and paths, and defaults only optional cache settings.
func Default() Config {
	return Config{Version: 1, DNS: DNS{Listen: []string{"127.0.0.1:5353"}}, Admin: Admin{Listen: "127.0.0.1:8080"}, Paths: Paths{DataDir: "./data", SecretsDir: "./secrets"}, Cache: Cache{Bytes: 8 << 20, Shards: 4, MaxNegativeTTLSeconds: 300, StaleTTLSeconds: 30, StaleMode: "immediate", MaxStaleSeconds: 3600}, Statistics: DefaultStatistics()}
}

func Parse(source []byte) (*Document, error) {
	if len(source) > maxConfigBytes {
		return nil, fmt.Errorf("configuration exceeds 1 MiB")
	}
	d := &Document{source: bytes.Clone(source)}
	d.value.Cache = Default().Cache
	d.value.Statistics = DefaultStatistics()
	dec := yaml.NewDecoder(bytes.NewReader(source))
	dec.KnownFields(true)
	if err := dec.Decode(&d.value); err != nil {
		return nil, fmt.Errorf("configuration: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("configuration: expected exactly one YAML document")
	}
	if err := yaml.Unmarshal(source, &d.root); err != nil {
		return nil, err
	}
	if err := checkNodes(&d.root); err != nil {
		return nil, err
	}
	if err := Validate(d.value); err != nil {
		field := strings.SplitN(err.Error(), ":", 2)[0]
		field = strings.ReplaceAll(strings.ReplaceAll(field, "[", "."), "]", "")
		path := strings.Split(field, ".")
		for len(path) > 0 {
			if n, e := d.node(path); e == nil {
				return nil, fmt.Errorf("configuration line %d column %d: %w", n.Line, n.Column, err)
			}
			path = path[:len(path)-1]
		}
		return nil, fmt.Errorf("configuration line 1: %w", err)
	}
	return d, nil
}

// Keep the editable schema unambiguous: no alias/merge expansion, custom tags,
// or null-as-default surprises. Ordinary YAML block and flow collections work.
func checkNodes(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode || n.Anchor != "" || n.Tag == "!!merge" || n.Tag == "!!null" || n.Style&yaml.TaggedStyle != 0 {
		return fmt.Errorf("configuration line %d: aliases, anchors, merges, explicit tags and null are unsupported", n.Line)
	}
	for _, child := range n.Content {
		if err := checkNodes(child); err != nil {
			return err
		}
	}
	return nil
}

func (d *Document) Config() Config {
	c := d.value
	c.Naming.DNSGuesses = c.Naming.DNSGuesses.Clone()
	c.DHCP = c.DHCP.Clone()
	c.Admin.AllowedHosts = append([]string(nil), c.Admin.AllowedHosts...)
	c.DNS.Listen = append([]string(nil), c.DNS.Listen...)
	c.DNS.Upstreams = append([]string(nil), c.DNS.Upstreams...)
	if c.DNS.BootstrapDNS != nil {
		c.DNS.BootstrapDNS = append([]string{}, c.DNS.BootstrapDNS...)
	}
	c.DNS.Fallback = append([]string(nil), c.DNS.Fallback...)
	c.Lists = append([]lists.Subscription(nil), c.Lists...)
	for i := range c.Lists {
		if o := c.Lists[i].BuiltinOverrides; o != nil {
			c.Lists[i].BuiltinOverrides = &lists.BuiltinOverrides{
				Additions:  append([]string(nil), o.Additions...),
				Exclusions: append([]string(nil), o.Exclusions...),
			}
		}
	}
	c.Rules = append([]CustomRule(nil), c.Rules...)
	c.Records = append([]Record(nil), c.Records...)
	c.Clients = append([]ClientOverride(nil), c.Clients...)
	c.cloneClientPolicy()
	c.Zones = append([]localdns.Zone(nil), c.Zones...)
	return c
}
func (d *Document) Bytes() []byte { return bytes.Clone(d.source) }

func (d *Document) parseEdit(source []byte) (*Document, error) {
	next, err := Parse(source)
	if err != nil {
		return nil, err
	}
	if err := dhcp.ValidateTransition(d.value.DHCP, next.value.DHCP); err != nil {
		return nil, err
	}
	return next, nil
}
func (d *Document) Revision() string { return revision(d.source) }
func revision(b []byte) string       { return fmt.Sprintf("%x", sha256.Sum256(b)) }
