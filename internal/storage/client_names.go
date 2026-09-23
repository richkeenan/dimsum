package storage

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/richkeenan/dimsum/internal/clients"
)

type clientNameKey struct{ kind, address string }

func nameKey(n clients.Name) (clientNameKey, error) {
	key := clientNameKey{address: n.Address.Unmap().String()}
	switch n.Source {
	case "mdns", "dns-sd", "spotify-connect":
		key.kind = "multicast"
	case "hosts", "router-ptr":
		key.kind = "resolved"
	default:
		return key, fmt.Errorf("invalid persisted name source %q", n.Source)
	}
	if !n.Address.IsValid() || n.Address.IsUnspecified() || n.Address.IsMulticast() || n.Address.Zone() != "" || n.Name == "" || len(n.Name) > 1024 || n.Updated.IsZero() || n.Negative ||
		n.Device != nil && (len(n.Device.Evidence) > 16 || n.Device.DNSGuess != nil) {
		return key, fmt.Errorf("invalid persisted client name")
	}
	return key, nil
}

// SaveClientNames atomically checkpoints the bounded derived cache. Unchanged
// rows do not write WAL pages. Query history retention cannot delete this table.
func (d *DB) SaveClientNames(ctx context.Context, scope string, names []clients.Name) error {
	if _, err := hex.DecodeString(scope); err != nil || len(scope) != 64 || len(names) > 8192 {
		return fmt.Errorf("invalid client name snapshot bounds")
	}
	payloads := make(map[clientNameKey][]byte, len(names))
	counts := map[string]int{}
	for _, n := range names {
		key, err := nameKey(n)
		if err != nil {
			return err
		}
		if _, exists := payloads[key]; exists {
			return fmt.Errorf("duplicate persisted client name")
		}
		n.Address = n.Address.Unmap()
		payload, err := json.Marshal(n)
		if err != nil || len(payload) > 65536 {
			return fmt.Errorf("invalid client name payload")
		}
		counts[key.kind]++
		if counts[key.kind] > 4096 {
			return fmt.Errorf("too many persisted client names")
		}
		payloads[key] = payload
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, err := d.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT kind,address FROM client_names LIMIT 8193`)
	if err != nil {
		return err
	}
	var removed []clientNameKey
	for rows.Next() {
		var key clientNameKey
		if err := rows.Scan(&key.kind, &key.address); err != nil {
			rows.Close()
			return err
		}
		if _, exists := payloads[key]; !exists {
			removed = append(removed, key)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, key := range removed {
		if _, err := tx.ExecContext(ctx, `DELETE FROM client_names WHERE kind=? AND address=?`, key.kind, key.address); err != nil {
			return err
		}
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO client_names(kind,address,scope,payload) VALUES(?,?,?,?)
ON CONFLICT(kind,address) DO UPDATE SET scope=excluded.scope,payload=excluded.payload
WHERE client_names.scope<>excluded.scope OR client_names.payload<>excluded.payload`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for key, payload := range payloads {
		if _, err := stmt.ExecContext(ctx, key.kind, key.address, scope, payload); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LoadClientNames reads only the bounded derived cache, not historical queries.
func (d *DB) LoadClientNames(ctx context.Context) (string, []clients.Name, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rows, err := d.read.QueryContext(ctx, `SELECT kind,address,scope,payload FROM client_names ORDER BY kind,address LIMIT 8193`)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	var scope string
	var names []clients.Name
	for rows.Next() {
		var key clientNameKey
		var rowScope string
		var payload []byte
		if err := rows.Scan(&key.kind, &key.address, &rowScope, &payload); err != nil {
			return "", nil, err
		}
		if len(names) >= 8192 || len(payload) > 65536 || scope != "" && scope != rowScope {
			return "", nil, fmt.Errorf("invalid persisted client name snapshot")
		}
		var n clients.Name
		if err := json.Unmarshal(payload, &n); err != nil {
			return "", nil, fmt.Errorf("invalid persisted client name: %w", err)
		}
		if actual, err := nameKey(n); err != nil || actual != key {
			return "", nil, fmt.Errorf("invalid persisted client name identity")
		}
		scope = rowScope
		names = append(names, n)
	}
	return scope, names, rows.Err()
}
