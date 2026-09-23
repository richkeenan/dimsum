package app

import (
	"context"
	"time"

	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/storage"
)

// Owned by the service lifecycle: initialize before workers start, then access
// from the checkpoint worker, then flush after that worker has stopped.
type clientNamePersistence struct {
	db       *storage.DB
	names    *clients.Manager
	restored bool
}

func (p *clientNamePersistence) restore(ctx context.Context) {
	scope, saved, err := p.db.LoadClientNames(ctx)
	if err == nil {
		p.names.RestoreNames(scope, saved, time.Now())
		p.restored = true
	}
	p.names.SetPersistenceError(err)
}

func (p *clientNamePersistence) save(ctx context.Context) {
	if !p.restored {
		p.restore(ctx)
		if !p.restored {
			return // Never replace unread durable state with a partial live cache.
		}
	}
	scope, saved := p.names.NameSnapshot(time.Now())
	err := p.db.SaveClientNames(ctx, scope, saved)
	p.names.SetPersistenceError(err)
}

// Checkpoint outside the DNS and discovery workers. Graceful shutdown does a
// final save after those workers stop, before the shared database is closed.
func (p *clientNamePersistence) run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.save(ctx)
		}
	}
}
