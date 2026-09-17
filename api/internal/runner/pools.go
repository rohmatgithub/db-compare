package runner

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"dbcompare/internal/engine"
	"dbcompare/internal/store"
)

// Pools shares one connection pool per saved connection across all runs, so
// MaxConns bounds the load this server puts on each compared database no
// matter how many runs use it.
type Pools struct {
	store    *store.Store
	maxConns int

	mu      sync.Mutex
	entries map[int64]*poolEntry
}

type poolEntry struct {
	db        *sql.DB
	dialect   engine.Dialect
	info      engine.ConnInfo
	updatedAt time.Time
}

func NewPools(st *store.Store, maxConns int) *Pools {
	return &Pools{store: st, maxConns: maxConns, entries: map[int64]*poolEntry{}}
}

// Get returns the pool for a connection, reopening it when the connection
// was edited since the pool was created.
func (p *Pools) Get(ctx context.Context, c store.Connection) (*sql.DB, engine.Dialect, engine.ConnInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entries[c.ID]; e != nil {
		if e.updatedAt.Equal(c.UpdatedAt) {
			return e.db, e.dialect, e.info, nil
		}
		delete(p.entries, c.ID)
		go e.db.Close()
	}

	password, err := p.store.ConnectionPassword(ctx, c.ID)
	if err != nil {
		return nil, nil, engine.ConnInfo{}, err
	}
	info := c.ConnInfo(password)
	db, dialect, err := engine.Open(info)
	if err != nil {
		return nil, nil, engine.ConnInfo{}, err
	}
	db.SetMaxOpenConns(p.maxConns)
	db.SetMaxIdleConns(p.maxConns)
	db.SetConnMaxIdleTime(5 * time.Minute)
	p.entries[c.ID] = &poolEntry{db: db, dialect: dialect, info: info, updatedAt: c.UpdatedAt}
	return db, dialect, info, nil
}

// Invalidate closes the pool of a deleted or edited connection. Queries
// already running on it are allowed to finish.
func (p *Pools) Invalidate(id int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entries[id]; e != nil {
		delete(p.entries, id)
		go e.db.Close()
	}
}

func (p *Pools) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, e := range p.entries {
		e.db.Close()
		delete(p.entries, id)
	}
}
