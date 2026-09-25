package storage

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/richkeenan/dimsum/internal/stats"
)

type DomainActivityRow struct {
	Corroborated time.Time
	Address      netip.Addr
	Domain       string
	First, Last  time.Time
	Count        uint64
}

// DomainActivity reads only whitelisted domain aggregates, never packet payloads.
// The secondary domain/time index avoids scanning every client's full history.
// A truncated result must not be used for attribution: missing rows can conflict.
func (d *DB) DomainActivity(ctx context.Context, start, end time.Time, exact, suffix []string) ([]DomainActivityRow, bool, error) {
	if !end.After(start) || end.Sub(start) > 24*time.Hour || len(exact)+len(suffix) == 0 || len(exact) > 4096 || len(suffix) > 32 {
		return nil, false, fmt.Errorf("invalid domain activity bounds")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var clauses []string
	var args []any
	for _, names := range []struct {
		values []string
		suffix bool
	}{{exact, false}, {suffix, true}} {
		var placeholders []string
		for _, name := range names.values {
			n, err := policy.NormalizeName(name)
			if err != nil {
				return nil, false, err
			}
			wire := make([]byte, 255)
			wire = wire[:n.CopyWire(wire)]
			if names.suffix {
				clauses = append(clauses, "substr(name,-?)=?")
				args = append(args, len(wire), wire)
			} else {
				placeholders = append(placeholders, "?")
				args = append(args, wire)
			}
		}
		if len(placeholders) > 0 {
			clauses = append(clauses, "name IN ("+strings.Join(placeholders, ",")+")")
		}
	}
	query := `WITH activity AS MATERIALIZED (SELECT c.address,n.name,e.client_id,e.domain_id,MIN(e.timestamp) AS first,MAX(e.timestamp) AS last,COUNT(*) AS count
FROM query_events e INDEXED BY events_domain_time JOIN domains n ON n.id=e.domain_id JOIN clients c ON c.id=e.client_id
WHERE e.domain_id IN (SELECT id FROM domains WHERE ` + strings.Join(clauses, " OR ") + `)
AND e.timestamp>=? AND e.timestamp<? AND e.timestamp >= (SELECT value FROM storage_meta WHERE key='detail_cutoff')
AND e.qclass=1 AND e.outcome<>? GROUP BY e.client_id,e.domain_id ORDER BY e.client_id,e.domain_id LIMIT 4097)
SELECT address,name,first,last,count,COALESCE((SELECT MAX(p.timestamp) FROM query_events p INDEXED BY events_client_time
WHERE p.client_id=activity.client_id AND p.domain_id=activity.domain_id AND p.timestamp>=activity.first AND p.timestamp<=activity.last-30000000
AND p.qclass=1 AND p.outcome<>?),0) FROM activity`
	args = append(args, start.UnixMicro(), end.UnixMicro(), stats.AdmissionRejected)
	args = append(args, stats.AdmissionRejected)
	rows, err := d.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := []DomainActivityRow{}
	for rows.Next() {
		var address, wire []byte
		var first, last, corroborated int64
		var r DomainActivityRow
		if err = rows.Scan(&address, &wire, &first, &last, &r.Count, &corroborated); err != nil {
			return nil, false, err
		}
		a, ok := netip.AddrFromSlice(address)
		if !ok {
			continue
		}
		n, err := policy.NameFromWire(wire)
		if err != nil {
			continue
		}
		r.Address = a.Unmap()
		r.Domain = n.Display()
		r.First = time.UnixMicro(first).UTC()
		r.Last = time.UnixMicro(last).UTC()
		if corroborated != 0 {
			r.Corroborated = time.UnixMicro(corroborated).UTC()
		}
		result = append(result, r)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	if len(result) > 4096 {
		return nil, true, nil
	}
	return result, false, nil
}
