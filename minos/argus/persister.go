package argus

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Persister persists per-task Argus state so a Minos restart can resume
// stall and budget enforcement from where it left off. Two
// implementations ship: MemPersister (tests, local dev) and PGPersister
// (production, against the shared Postgres LXC).
type Persister interface {
	// Save writes the per-task state. Implementations MUST be safe for
	// concurrent calls.
	Save(ctx context.Context, taskID uuid.UUID, st *State) error
	// Delete removes the per-task state — called when Argus stops
	// tracking a task (terminal transition).
	Delete(ctx context.Context, taskID uuid.UUID) error
	// Load returns all persisted states. Argus calls this once at
	// startup to rehydrate its in-memory map.
	Load(ctx context.Context) ([]*State, error)
}

// MemPersister is an in-memory persister for tests and local dev.
type MemPersister struct {
	mu     sync.Mutex
	states map[uuid.UUID]*State
}

// NewMemPersister returns an empty MemPersister.
func NewMemPersister() *MemPersister {
	return &MemPersister{states: map[uuid.UUID]*State{}}
}

// Save implements Persister.
func (p *MemPersister) Save(_ context.Context, taskID uuid.UUID, st *State) error {
	if st == nil {
		return fmt.Errorf("argus mem persister: nil state")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	clone := *st
	p.states[taskID] = &clone
	return nil
}

// Delete implements Persister.
func (p *MemPersister) Delete(_ context.Context, taskID uuid.UUID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.states, taskID)
	return nil
}

// Load implements Persister.
func (p *MemPersister) Load(_ context.Context) ([]*State, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*State, 0, len(p.states))
	for _, s := range p.states {
		clone := *s
		out = append(out, &clone)
	}
	return out, nil
}

// PGPersister persists Argus state to the argus.task_states table
// (migration 0007). Encodes the State struct as JSON in a single column —
// keeps the schema simple while the rules engine evolves.
type PGPersister struct {
	pool *pgxpool.Pool
}

// NewPGPersister wraps a pgxpool.Pool. The caller is responsible for
// running the goose migrations before the persister sees traffic.
func NewPGPersister(pool *pgxpool.Pool) *PGPersister {
	return &PGPersister{pool: pool}
}

// Save implements Persister with INSERT ... ON CONFLICT DO UPDATE.
func (p *PGPersister) Save(ctx context.Context, taskID uuid.UUID, st *State) error {
	body, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("argus pg persister: marshal: %w", err)
	}
	const q = `
INSERT INTO argus.task_states (task_id, state, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (task_id) DO UPDATE
   SET state = EXCLUDED.state, updated_at = now()`
	if _, err := p.pool.Exec(ctx, q, taskID, body); err != nil {
		return fmt.Errorf("argus pg persister: upsert: %w", err)
	}
	return nil
}

// Delete implements Persister.
func (p *PGPersister) Delete(ctx context.Context, taskID uuid.UUID) error {
	if _, err := p.pool.Exec(ctx, `DELETE FROM argus.task_states WHERE task_id = $1`, taskID); err != nil {
		return fmt.Errorf("argus pg persister: delete: %w", err)
	}
	return nil
}

// Load implements Persister.
func (p *PGPersister) Load(ctx context.Context) ([]*State, error) {
	rows, err := p.pool.Query(ctx, `SELECT state FROM argus.task_states`)
	if err != nil {
		return nil, fmt.Errorf("argus pg persister: query: %w", err)
	}
	defer rows.Close()
	var out []*State
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var st State
		if err := json.Unmarshal(body, &st); err != nil {
			return nil, fmt.Errorf("argus pg persister: unmarshal: %w", err)
		}
		out = append(out, &st)
	}
	return out, rows.Err()
}
