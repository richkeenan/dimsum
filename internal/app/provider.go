package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/control"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/richkeenan/dimsum/internal/storage"
)

type historyProvider struct {
	db   *storage.DB
	name func(netip.Addr) clients.Name
	now  func() time.Time
}

// NewHistoryProvider adapts retained SQLite history, never lifetime counters or
// fabricated samples. name must be a nonblocking lookup of cached naming state.
func NewHistoryProvider(db *storage.DB, name func(netip.Addr) clients.Name) control.Provider {
	return &historyProvider{db: db, name: name, now: time.Now}
}

type historyRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}
type historySummary struct {
	Range      historyRange `json:"range"`
	Queries    string       `json:"queries"`
	Blocked    string       `json:"blocked"`
	Fresh      string       `json:"fresh"`
	Stale      string       `json:"stale"`
	Rejected   string       `json:"rejected"`
	DurationUS string       `json:"duration_us"`
	Complete   bool         `json:"complete"`
	UpdatedAt  time.Time    `json:"updated_at"`
}
type historyQuery struct {
	ID               string    `json:"id"`
	BootID           string    `json:"boot_id"`
	Sequence         string    `json:"sequence"`
	Time             time.Time `json:"time"`
	Client           string    `json:"client"`
	ClientName       string    `json:"client_name"`
	ClientNameSource string    `json:"client_name_source"`
	ClientNameFresh  bool      `json:"client_name_fresh"`
	Name             string    `json:"name"`
	QType            string    `json:"qtype"`
	QTypeCode        uint16    `json:"qtype_code"`
	QClass           uint16    `json:"qclass"`
	Outcome          string    `json:"outcome"`
	RCode            uint16    `json:"rcode"`
	DurationUS       string    `json:"duration_us"`
	Generation       string    `json:"generation"`
	RuleID           string    `json:"rule_id"`
	SourceID         string    `json:"source_id"`
	UpstreamID       string    `json:"upstream_id"`
	Flags            uint32    `json:"flags"`
}
type historyQueries struct {
	Items      []historyQuery `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
	Complete   bool           `json:"complete"`
	Range      historyRange   `json:"range"`
	UpdatedAt  time.Time      `json:"updated_at"`
}
type historyDetail struct {
	historyQuery
	Alias                     *string   `json:"alias,omitempty"`
	AliasAvailable            bool      `json:"alias_available"`
	RuleDescription           string    `json:"rule_description,omitempty"`
	RuleDescriptionAvailable  bool      `json:"rule_description_available"`
	AliasChainAvailable       bool      `json:"alias_chain_available"`
	UpstreamAttemptsAvailable bool      `json:"upstream_attempts_available"`
	CacheAgeAvailable         bool      `json:"cache_age_available"`
	ADTrustAvailable          bool      `json:"ad_trust_available"`
	UpdatedAt                 time.Time `json:"updated_at"`
}
type rankedClient struct {
	Address string `json:"address"`
	Name    string `json:"name"`
	Count   string `json:"count"`
}
type rankedDomain struct {
	Name  string `json:"name"`
	Count string `json:"count"`
}
type historyRankings struct {
	Clients   []rankedClient `json:"clients"`
	Domains   []rankedDomain `json:"domains"`
	Complete  bool           `json:"complete"`
	Range     historyRange   `json:"range"`
	UpdatedAt time.Time      `json:"updated_at"`
}
type historyPoint struct {
	Time       time.Time         `json:"time"`
	Outcomes   map[string]string `json:"outcomes"`
	DurationUS *string           `json:"duration_us"`
	Histogram  []string          `json:"histogram"`
	Complete   bool              `json:"complete"`
	Gap        bool              `json:"gap"`
}
type historySeries struct {
	Points            []historyPoint `json:"points"`
	ResolutionSeconds int64          `json:"resolution_seconds"`
	Complete          bool           `json:"complete"`
	Range             historyRange   `json:"range"`
	UpdatedAt         time.Time      `json:"updated_at"`
}
type observedClient struct {
	Address    string    `json:"address"`
	Name       string    `json:"name"`
	NameSource string    `json:"name_source"`
	NameFresh  bool      `json:"name_fresh"`
	Count      string    `json:"count"`
	Blocked    string    `json:"blocked"`
	LastSeen   time.Time `json:"last_seen"`
}
type historyClients struct {
	Items     []observedClient `json:"items"`
	Complete  bool             `json:"complete"`
	Truncated bool             `json:"truncated"`
	Range     historyRange     `json:"range"`
	UpdatedAt time.Time        `json:"updated_at"`
}

var outcomeNames = [stats.OutcomeCount]string{"local", "blocked", "cache", "stale", "forwarded", "error", "rejected"}
var qtypeNames = map[uint16]string{1: "A", 2: "NS", 5: "CNAME", 6: "SOA", 12: "PTR", 15: "MX", 16: "TXT", 28: "AAAA", 33: "SRV", 43: "DS", 46: "RRSIG", 47: "NSEC", 48: "DNSKEY", 50: "NSEC3", 64: "SVCB", 65: "HTTPS", 255: "ANY", 257: "CAA"}

func decimal(n uint64) string      { return strconv.FormatUint(n, 10) }
func invalid(message string) error { return fmt.Errorf("%w: %s", control.BadRequest, message) }
func historyError(e error) error {
	if e == nil {
		return nil
	}
	return fmt.Errorf("%w: history: %w", control.Unavailable, e)
}
func (h *historyProvider) available() error {
	if h.db == nil {
		return control.Unavailable
	}
	return nil
}
func (h *historyProvider) named(address netip.Addr) clients.Name {
	address = address.Unmap()
	if h.name == nil {
		return clients.Name{Address: address, Source: "unknown"}
	}
	v := h.name(address)
	v.Address = address
	if v.Source == "" {
		v.Source = "unknown"
	}
	return v
}

func checkParams(q url.Values, extra ...string) error {
	allowed := map[string]bool{"from": true, "to": true}
	for _, key := range extra {
		allowed[key] = true
	}
	for key, values := range q {
		if !allowed[key] {
			return invalid("unsupported parameter: " + key)
		}
		if len(values) != 1 || len(values[0]) > 2048 {
			return invalid("parameter must have one bounded value: " + key)
		}
	}
	return nil
}
func (h *historyProvider) window(q url.Values) (historyRange, error) {
	end := h.now().UTC().Truncate(time.Minute)
	parse := func(value string) (time.Time, error) {
		t, e := time.Parse(time.RFC3339Nano, value)
		if e != nil {
			return t, invalid("from/to must be RFC3339 UTC timestamps")
		}
		_, offset := t.Zone()
		if offset != 0 {
			return t, invalid("from/to must use UTC")
		}
		if t.Year() < 1970 {
			return t, invalid("from/to must be on or after 1970")
		}
		if t.Nanosecond()%1000 != 0 {
			return t, invalid("from/to precision must not exceed microseconds")
		}
		return t.UTC(), nil
	}
	var e error
	if value, ok := q["to"]; ok {
		end, e = parse(value[0])
		if e != nil {
			return historyRange{}, e
		}
	}
	start := end.Add(-time.Hour)
	if value, ok := q["from"]; ok {
		start, e = parse(value[0])
		if e != nil {
			return historyRange{}, e
		}
	}
	if !end.After(start) || end.Sub(start) > 366*24*time.Hour {
		return historyRange{}, invalid("range must be positive, half-open, and at most 366 days")
	}
	return historyRange{start, end}, nil
}
func parseLimit(q url.Values) (int, error) {
	if _, ok := q["limit"]; !ok {
		return 100, nil
	}
	n, e := strconv.Atoi(q.Get("limit"))
	if e != nil || n < 1 || n > 200 {
		return 0, invalid("limit must be 1–200")
	}
	return n, nil
}

func (h *historyProvider) Summary(ctx context.Context, q url.Values) (any, error) {
	if e := h.available(); e != nil {
		return nil, e
	}
	if e := checkParams(q); e != nil {
		return nil, e
	}
	window, e := h.window(q)
	if e != nil {
		return nil, e
	}
	s, e := h.db.Summary(ctx, window.From, window.To)
	if e != nil {
		return nil, historyError(e)
	}
	return historySummary{window, decimal(s.Admitted), decimal(s.Blocked), decimal(s.FreshCache), decimal(s.StaleCache), decimal(s.Rejected), decimal(s.Duration), s.Complete, h.now().UTC()}, nil
}

type historyCursor struct {
	Version  int            `json:"v"`
	From     time.Time      `json:"from"`
	To       time.Time      `json:"to"`
	Filter   string         `json:"filter"`
	Position storage.Cursor `json:"position"`
}

func decodeHistoryCursor(raw string) (historyCursor, error) {
	var c historyCursor
	if len(raw) > 2048 {
		return c, invalid("cursor exceeds size limit")
	}
	b, e := base64.RawURLEncoding.Strict().DecodeString(raw)
	if e != nil {
		return c, invalid("malformed cursor")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, invalid("malformed cursor")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, invalid("malformed cursor")
	}
	if c.Version != 1 || c.Position.ID <= 0 || c.Position.Ceiling < c.Position.ID || c.Position.Timestamp < c.From.UnixMicro() || c.Position.Timestamp >= c.To.UnixMicro() {
		return c, invalid("invalid cursor position or version")
	}
	return c, nil
}
func queryFingerprint(o storage.QueryOptions) string {
	// Marshal a fixed struct instead of parameter spelling/order. Normalized
	// address, qtype and name equivalents therefore retain cursor compatibility.
	value := struct {
		From, To       string
		Domain, Client []byte
		Outcome        *stats.Outcome
		QType          *uint16
		Filters        storage.HistoryFilters
	}{o.Start.Format(time.RFC3339Nano), o.End.Format(time.RFC3339Nano), o.Domain, o.Client, o.Outcome, o.QType, o.HistoryFilters}
	b, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func parseQType(value string) (uint16, error) {
	value = strings.ToUpper(value)
	for number, name := range qtypeNames {
		if name == value {
			return number, nil
		}
	}
	n, e := strconv.ParseUint(strings.TrimPrefix(value, "TYPE"), 10, 16)
	if e != nil || n == 0 {
		return 0, invalid("qtype must be a DNS type name or code 1–65535")
	}
	return uint16(n), nil
}

// presentationWire accepts the byte-safe Display representation, including
// decimal escapes, so a displayed binary label can be used for an exact filter.
func presentationWire(value string) ([]byte, error) {
	if value == "." {
		return []byte{0}, nil
	}
	if !strings.Contains(value, "\\") {
		n, e := policy.NormalizeName(value)
		if e != nil {
			return nil, invalid("invalid exact name filter")
		}
		value = n.Display()
	}
	value = strings.TrimSuffix(value, ".")
	var wire, label []byte
	appendLabel := func() error {
		if len(label) == 0 || len(label) > 63 {
			return invalid("invalid exact name label")
		}
		wire = append(wire, byte(len(label)))
		wire = append(wire, label...)
		label = nil
		return nil
	}
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '.':
			if e := appendLabel(); e != nil {
				return nil, e
			}
		case '\\':
			if i+3 >= len(value) {
				return nil, invalid("name escapes require three decimal digits")
			}
			digits := value[i+1 : i+4]
			for _, digit := range digits {
				if digit < '0' || digit > '9' {
					return nil, invalid("name escapes require three decimal digits")
				}
			}
			n, e := strconv.ParseUint(digits, 10, 8)
			if e != nil {
				return nil, invalid("invalid name escape")
			}
			label = append(label, byte(n))
			i += 3
		default:
			label = append(label, value[i])
		}
	}
	if e := appendLabel(); e != nil {
		return nil, e
	}
	wire = append(wire, 0)
	if _, e := policy.NameFromWire(wire); e != nil {
		return nil, invalid("invalid exact name wire length")
	}
	for i := 0; i < len(wire); {
		size := int(wire[i])
		if size == 0 {
			break
		}
		for j := i + 1; j < i+1+size; j++ {
			if wire[j] >= 'A' && wire[j] <= 'Z' {
				wire[j] += 'a' - 'A'
			}
		}
		i += size + 1
	}
	return wire, nil
}
func (h *historyProvider) queryOptions(q url.Values) (storage.QueryOptions, error) {
	var o storage.QueryOptions
	if e := checkParams(q, "limit", "cursor", "name", "client", "outcome", "qtype", "boot_id", "generation", "rule_id", "source_id", "upstream_id"); e != nil {
		return o, e
	}
	params := make(url.Values, len(q))
	for key, values := range q {
		params[key] = append([]string(nil), values...)
	}
	var cursor *historyCursor
	if raw, ok := params["cursor"]; ok {
		c, e := decodeHistoryCursor(raw[0])
		if e != nil {
			return o, e
		}
		cursor = &c
		if _, ok = params["from"]; !ok {
			params.Set("from", c.From.Format(time.RFC3339Nano))
		}
		if _, ok = params["to"]; !ok {
			params.Set("to", c.To.Format(time.RFC3339Nano))
		}
	}
	window, e := h.window(params)
	if e != nil {
		return o, e
	}
	o.Start, o.End = window.From, window.To
	o.Limit, e = parseLimit(params)
	if e != nil {
		return o, e
	}
	if value, ok := params["name"]; ok {
		o.Domain, e = presentationWire(value[0])
		if e != nil {
			return o, e
		}
	}
	if value, ok := params["client"]; ok {
		a, e := netip.ParseAddr(value[0])
		if e != nil || a.Zone() != "" {
			return o, invalid("client must be a literal IP without zone")
		}
		b := a.Unmap().As16()
		o.Client = append([]byte(nil), b[:]...)
	}
	if value, ok := params["outcome"]; ok {
		found := false
		for index, name := range outcomeNames {
			if value[0] == name {
				outcome := stats.Outcome(index)
				o.Outcome = &outcome
				found = true
				break
			}
		}
		if !found {
			return o, invalid("unsupported outcome")
		}
	}
	if value, ok := params["qtype"]; ok {
		n, e := parseQType(value[0])
		if e != nil {
			return o, e
		}
		o.QType = &n
	}
	for _, key := range []string{"boot_id", "source_id", "generation", "rule_id", "upstream_id"} {
		values, ok := params[key]
		if !ok {
			continue
		}
		value := values[0]
		if value == "" {
			return o, invalid(key + " must not be empty")
		}
		switch key {
		case "boot_id":
			o.BootID = value
		case "source_id":
			o.SourceID = value
		default:
			bits := 32
			if key == "upstream_id" {
				bits = 16
			}
			n, err := strconv.ParseUint(value, 10, bits)
			if err != nil || strconv.FormatUint(n, 10) != value {
				return o, invalid(key + " must be a canonical unsigned decimal")
			}
			v := uint32(n)
			switch key {
			case "generation":
				o.Generation = &v
			case "rule_id":
				o.RuleID = &v
			case "upstream_id":
				u := uint16(n)
				o.UpstreamID = &u
			}
		}
	}
	if err := o.HistoryFilters.Validate(); err != nil {
		return o, invalid(err.Error())
	}
	if cursor != nil {
		if cursor.Filter != queryFingerprint(o) {
			return o, invalid("cursor does not match filters or range")
		}
		o.Cursor = &cursor.Position
	}
	return o, nil
}
func (h *historyProvider) queryRow(row storage.Row) (historyQuery, error) {
	e := row.Event
	n, err := policy.NameFromWire(e.QName[:e.QNameLength])
	identityMissing := e.Outcome == stats.AdmissionRejected && e.QNameLength == 0
	if err != nil && !identityMissing {
		return historyQuery{}, historyError(err)
	}
	if e.Outcome >= stats.OutcomeCount {
		return historyQuery{}, historyError(errors.New("invalid retained outcome"))
	}
	address := netip.AddrFrom16(e.Client).Unmap()
	name := h.named(address)
	if identityMissing {
		return historyQuery{ID: strconv.FormatInt(row.ID, 10), BootID: row.Boot, Sequence: decimal(e.Sequence), Time: time.UnixMicro(e.Timestamp).UTC(), Outcome: outcomeNames[e.Outcome], DurationUS: decimal(uint64(e.Duration)), ClientNameSource: "unavailable", Generation: decimal(uint64(e.Generation)), RuleID: "0", UpstreamID: "0"}, nil
	}
	qtype := qtypeNames[e.QType]
	if qtype == "" {
		qtype = "TYPE" + strconv.Itoa(int(e.QType))
	}
	return historyQuery{ID: strconv.FormatInt(row.ID, 10), BootID: row.Boot, Sequence: decimal(e.Sequence), Time: time.UnixMicro(e.Timestamp).UTC(), Client: address.String(), ClientName: name.Name, ClientNameSource: name.Source, ClientNameFresh: name.Fresh, Name: n.Display(), QType: qtype, QTypeCode: e.QType, QClass: e.QClass, Outcome: outcomeNames[e.Outcome], RCode: e.RCode, DurationUS: decimal(uint64(e.Duration)), Generation: decimal(uint64(e.Generation)), RuleID: decimal(uint64(e.RuleID)), SourceID: row.SourceID, UpstreamID: decimal(uint64(e.UpstreamID)), Flags: e.Flags}, nil
}
func (h *historyProvider) Queries(ctx context.Context, q url.Values) (any, error) {
	if e := h.available(); e != nil {
		return nil, e
	}
	o, e := h.queryOptions(q)
	if e != nil {
		return nil, e
	}
	page, e := h.db.Query(ctx, o)
	if e != nil {
		return nil, historyError(e)
	}
	result := historyQueries{Items: []historyQuery{}, Complete: page.Complete, Range: historyRange{o.Start, o.End}, UpdatedAt: h.now().UTC()}
	for _, row := range page.Rows {
		item, e := h.queryRow(row)
		if e != nil {
			return nil, e
		}
		result.Items = append(result.Items, item)
	}
	if page.Next != nil {
		b, e := json.Marshal(historyCursor{1, o.Start, o.End, queryFingerprint(o), *page.Next})
		if e != nil {
			return nil, historyError(e)
		}
		result.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return result, nil
}
func (h *historyProvider) Query(ctx context.Context, q url.Values) (any, error) {
	if e := h.available(); e != nil {
		return nil, e
	}
	for key, values := range q {
		if key != "id" || len(values) != 1 || len(values[0]) > 20 {
			return nil, invalid("detail requires only a decimal id")
		}
	}
	id, e := strconv.ParseInt(q.Get("id"), 10, 64)
	if e != nil || id <= 0 || strconv.FormatInt(id, 10) != q.Get("id") {
		return nil, invalid("id must be a positive decimal string")
	}
	row, e := h.db.QueryByID(ctx, id)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, control.NotFound
	}
	if e != nil {
		return nil, historyError(e)
	}
	item, e := h.queryRow(row)
	if e != nil {
		return nil, e
	}
	result := historyDetail{historyQuery: item, RuleDescription: row.RuleDescription, RuleDescriptionAvailable: row.RuleDescription != "", UpdatedAt: h.now().UTC()}
	if len(row.Alias) > 0 {
		alias, e := policy.NameFromWire(row.Alias)
		if e != nil {
			return nil, historyError(e)
		}
		display := alias.Display()
		result.Alias = &display
		result.AliasAvailable = true
	}
	return result, nil
}
func (h *historyProvider) Rankings(ctx context.Context, q url.Values) (any, error) {
	if e := h.available(); e != nil {
		return nil, e
	}
	if e := checkParams(q); e != nil {
		return nil, e
	}
	window, e := h.window(q)
	if e != nil {
		return nil, e
	}
	rankings, e := h.db.Rankings(ctx, window.From, window.To)
	if e != nil {
		return nil, historyError(e)
	}
	result := historyRankings{Clients: []rankedClient{}, Domains: []rankedDomain{}, Complete: rankings.Complete, Range: window, UpdatedAt: h.now().UTC()}
	for _, row := range rankings.Clients {
		b := []byte(row.Key)
		if len(b) != 16 {
			return nil, historyError(errors.New("invalid retained client identity"))
		}
		address := netip.AddrFrom16([16]byte(b)).Unmap()
		result.Clients = append(result.Clients, rankedClient{address.String(), h.named(address).Name, decimal(row.Count)})
	}
	for _, row := range rankings.BlockedDomains {
		n, e := policy.NameFromWire([]byte(row.Key))
		if e != nil {
			return nil, historyError(e)
		}
		result.Domains = append(result.Domains, rankedDomain{n.Display(), decimal(row.Count)})
	}
	return result, nil
}
func (h *historyProvider) Timeseries(ctx context.Context, q url.Values) (any, error) {
	if e := h.available(); e != nil {
		return nil, e
	}
	if e := checkParams(q, "resolution_seconds"); e != nil {
		return nil, e
	}
	window, e := h.window(q)
	if e != nil {
		return nil, e
	}
	width := time.Minute
	count := func(width time.Duration) int64 {
		end := window.To.Truncate(width)
		if end.Before(window.To) {
			end = end.Add(width)
		}
		return int64(end.Sub(window.From.Truncate(width)) / width)
	}
	if value, ok := q["resolution_seconds"]; ok {
		switch value[0] {
		case "60":
			width = time.Minute
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
		return nil, invalid("timeseries allows at most 1500 buckets")
	}
	result := historySeries{Points: []historyPoint{}, ResolutionSeconds: int64(width / time.Second), Complete: true, Range: window}
	appendPoint := func(point storage.Point, complete bool) {
		duration := decimal(point.Duration)
		item := historyPoint{Time: time.UnixMicro(point.Timestamp).UTC(), Outcomes: make(map[string]string), DurationUS: &duration, Histogram: []string{}, Complete: complete}
		nonzero := false
		for index, count := range point.Outcomes {
			item.Outcomes[outcomeNames[index]] = decimal(count)
			nonzero = nonzero || count > 0
		}
		for _, n := range point.Histogram {
			item.Histogram = append(item.Histogram, decimal(n))
		}
		if !complete && !nonzero {
			item.Outcomes = nil
			item.DurationUS = nil
			item.Histogram = nil
			item.Gap = true
		}
		result.Points = append(result.Points, item)
		result.Complete = result.Complete && complete
	}
	partial := func(start, end time.Time) error {
		point, complete, e := h.db.PartialSeriesPoint(ctx, start, end)
		if e != nil {
			return historyError(e)
		}
		appendPoint(point, complete)
		return nil
	}
	cursor := window.From
	if cursor != cursor.Truncate(width) {
		end := cursor.Truncate(width).Add(width)
		if window.To.Before(end) {
			end = window.To
		}
		if e = partial(cursor, end); e != nil {
			return nil, e
		}
		cursor = end
	}
	alignedEnd := window.To.Truncate(width)
	if cursor.Before(alignedEnd) {
		series, e := h.db.Timeseries(ctx, cursor, alignedEnd, width)
		if e != nil {
			return nil, historyError(e)
		}
		for _, point := range series.Points {
			appendPoint(point, series.Complete)
		}
		cursor = alignedEnd
	}
	if cursor.Before(window.To) {
		if e = partial(cursor, window.To); e != nil {
			return nil, e
		}
	}
	result.UpdatedAt = h.now().UTC()
	return result, nil
}
func (h *historyProvider) Clients(ctx context.Context, q url.Values) (any, error) {
	if e := h.available(); e != nil {
		return nil, e
	}
	if e := checkParams(q, "limit"); e != nil {
		return nil, e
	}
	window, e := h.window(q)
	if e != nil {
		return nil, e
	}
	limit, e := parseLimit(q)
	if e != nil {
		return nil, e
	}
	page, e := h.db.GetClients(ctx, window.From, window.To, limit)
	if e != nil {
		return nil, historyError(e)
	}
	result := historyClients{Items: []observedClient{}, Complete: page.Complete, Truncated: page.Truncated, Range: window, UpdatedAt: h.now().UTC()}
	for _, row := range page.Items {
		address := netip.AddrFrom16(row.Address).Unmap()
		name := h.named(address)
		result.Items = append(result.Items, observedClient{address.String(), name.Name, name.Source, name.Fresh, decimal(row.Count), decimal(row.Blocked), row.LastSeen})
	}
	return result, nil
}
