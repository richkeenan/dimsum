package storage

import "errors"

const MaxSourceIDBytes = 512

// HistoryFilters scopes numeric identities to the configuration that assigned
// them. SourceID is an exact archived string and survives configuration deletion.
type HistoryFilters struct {
	BootID     string
	Generation *uint32
	RuleID     *uint32
	UpstreamID *uint16
	SourceID   string
}

func (f HistoryFilters) Validate() error {
	if len(f.BootID) > 128 || len(f.SourceID) > MaxSourceIDBytes {
		return errors.New("history identity exceeds size limit")
	}
	if (f.RuleID != nil || f.UpstreamID != nil) && (f.BootID == "" || f.Generation == nil) {
		return errors.New("rule_id and upstream_id require boot_id and generation")
	}
	return nil
}

func (f HistoryFilters) sql() (string, []any) {
	var clause string
	var args []any
	add := func(column string, value any) { clause += " AND " + column + "=?"; args = append(args, value) }
	if f.BootID != "" {
		add("e.boot_id", f.BootID)
	}
	if f.Generation != nil {
		add("e.generation", *f.Generation)
	}
	if f.RuleID != nil {
		add("e.rule_id", *f.RuleID)
	}
	if f.UpstreamID != nil {
		add("e.upstream_id", *f.UpstreamID)
	}
	if f.SourceID != "" {
		add("r.source_id", f.SourceID)
	}
	return clause, args
}
