package app

import (
	"context"
	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/storage"
	"time"
)

func refreshDNSGuesses(ctx context.Context, db *storage.DB, names *clients.Manager, now time.Time) error {
	view, exact, suffix := names.DNSGuessSelectors()
	rows, truncated, err := db.DomainActivity(ctx, now.Add(-clients.DNSGuessLifetime), now, exact, suffix)
	if err != nil {
		return err
	} // Prior evidence retains its original expiry.
	activity := make([]clients.DNSActivity, 0, len(rows))
	if !truncated {
		for _, r := range rows {
			activity = append(activity, clients.DNSActivity{Address: r.Address, Domain: r.Domain, First: r.First, Last: r.Last, Corroborated: r.Corroborated, Count: r.Count})
		}
	}
	names.ReplaceDNSActivityForView(view, activity)
	return nil
}

// History is already collected off the request path. Reuse it for both startup
// backfill and live updates, adding no parsing, IO or locking to DNS resolution.
func runDNSGuesses(ctx context.Context, db *storage.DB, names *clients.Manager) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = refreshDNSGuesses(ctx, db, names, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
