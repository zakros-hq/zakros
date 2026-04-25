// Package pgstore is the Postgres-backed mnemosyne/core.Store. It targets
// the shared Postgres LXC's mnemosyne schema (migration 0005).
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mcore "github.com/zakros-hq/zakros/mnemosyne/core"
)

// Store is the Postgres implementation.
type Store struct {
	pool *pgxpool.Pool
	// MaxContextRuns caps prior-summary context assembly; zero = unlimited.
	MaxContextRuns int
}

// New wraps an existing pgxpool.Pool; caller is responsible for running
// the goose migrations before any call lands.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, MaxContextRuns: 5}
}

// StoreRun satisfies mnemosyne/core.Store.
func (s *Store) StoreRun(ctx context.Context, rec *mcore.RunRecord) error {
	if rec == nil {
		return fmt.Errorf("pgstore: nil record")
	}
	id := rec.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	createdAt := rec.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	const q = `
INSERT INTO mnemosyne.run_records (
  id, task_id, run_id, project_id, task_type, outcome, summary, body, created_at
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9
)`
	_, err := s.pool.Exec(ctx, q,
		id, rec.TaskID, rec.RunID, rec.ProjectID, rec.TaskType,
		string(rec.Outcome), rec.Summary, rec.Body, createdAt,
	)
	if err != nil {
		return fmt.Errorf("pgstore: insert run record: %w", err)
	}
	return nil
}

// GetContext satisfies mnemosyne/core.Store. Phase 1 assembly: latest-N
// summaries newest-first, same shape as memstore.
func (s *Store) GetContext(ctx context.Context, projectID, taskType string) (*mcore.Context, error) {
	limit := s.MaxContextRuns
	if limit <= 0 {
		limit = 5
	}
	q := `
SELECT id, task_id, run_id, project_id, task_type, outcome, summary, body, created_at
FROM mnemosyne.run_records
WHERE project_id = $1`
	args := []any{projectID}
	if taskType != "" {
		q += ` AND task_type = $2`
		args = append(args, taskType)
	}
	q += fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d`, len(args)+1)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("pgstore: get context: %w", err)
	}
	defer rows.Close()
	var records []*mcore.RunRecord
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	return &mcore.Context{
		Ref:       fmt.Sprintf("pg:%s:%s:%d", projectID, taskType, len(records)),
		Body:      assembleSummaries(records),
		PriorRuns: len(records),
	}, nil
}

// GetRunsForTask satisfies mnemosyne/core.Store.
func (s *Store) GetRunsForTask(ctx context.Context, taskID uuid.UUID) ([]*mcore.RunRecord, error) {
	const q = `
SELECT id, task_id, run_id, project_id, task_type, outcome, summary, body, created_at
FROM mnemosyne.run_records WHERE task_id = $1 ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q, taskID)
	if err != nil {
		return nil, fmt.Errorf("pgstore: get runs for task: %w", err)
	}
	defer rows.Close()
	var out []*mcore.RunRecord
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanRun(r pgx.Row) (*mcore.RunRecord, error) {
	var rec mcore.RunRecord
	var outcome string
	var body []byte
	var summary *string
	if err := r.Scan(
		&rec.ID, &rec.TaskID, &rec.RunID, &rec.ProjectID, &rec.TaskType,
		&outcome, &summary, &body, &rec.CreatedAt,
	); err != nil {
		return nil, err
	}
	rec.Outcome = mcore.Outcome(outcome)
	if summary != nil {
		rec.Summary = *summary
	}
	rec.Body = body
	return &rec, nil
}

func assembleSummaries(records []*mcore.RunRecord) string {
	var sb []byte
	sb = append(sb, "Prior-run context (newest first):\n"...)
	for i, r := range records {
		sb = append(sb, fmt.Sprintf("\n[%d] run=%s outcome=%s at=%s\n",
			i+1, r.RunID.String()[:8], r.Outcome, r.CreatedAt.Format(time.RFC3339))...)
		if r.Summary != "" {
			sb = append(sb, r.Summary...)
			sb = append(sb, '\n')
		}
	}
	return string(sb)
}

// compile-time check
var _ mcore.Store = (*Store)(nil)

// Forces errors import so pgx errors.Is usage (if needed later) doesn't
// produce drift — currently unused, but retained to match the minos/storage
// pattern. Remove if the package gains other errors wrappers.
var _ = errors.New
