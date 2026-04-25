// Package pgstore is the Postgres-backed storage.Store implementation. It
// targets the shared Postgres LXC per architecture.md §4 VM Inventory and
// uses the `minos` schema created by migration 0001.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakros-hq/zakros/minos/storage"
	"github.com/zakros-hq/zakros/pkg/envelope"
)

// Store is the Postgres implementation of storage.Store.
type Store struct {
	pool *pgxpool.Pool
}

// New wraps an existing pgxpool.Pool in a Store. Callers are responsible for
// running Migrate before calling any Store method.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// InsertTask implements storage.Store.
func (s *Store) InsertTask(ctx context.Context, t *storage.Task) error {
	if t == nil {
		return fmt.Errorf("%w: nil task", storage.ErrConflict)
	}
	envJSON, err := json.Marshal(t.Envelope)
	if err != nil {
		return fmt.Errorf("pgstore: marshal envelope: %w", err)
	}
	state := t.State
	if state == "" {
		state = storage.StateQueued
	}
	createdAt := t.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	const q = `
INSERT INTO minos.tasks (
  id, parent_id, project_id, task_type, backend, state, priority,
  envelope, run_id, pod_name, created_at, started_at, finished_at
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
)`
	_, err = s.pool.Exec(ctx, q,
		t.ID, t.ParentID, t.ProjectID, t.TaskType, t.Backend, state, t.Priority,
		envJSON, t.RunID, t.PodName, createdAt, t.StartedAt, t.FinishedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: task %s already exists", storage.ErrConflict, t.ID)
		}
		return fmt.Errorf("pgstore: insert: %w", err)
	}
	return nil
}

// GetTask implements storage.Store.
func (s *Store) GetTask(ctx context.Context, id uuid.UUID) (*storage.Task, error) {
	const q = `
SELECT id, parent_id, project_id, task_type, backend, state, priority,
       envelope, run_id, pod_name, created_at, started_at, finished_at, pr_url,
       state_changed_at, reminded_at
FROM minos.tasks WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, id)
	task, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, id)
	}
	return task, err
}

// ListTasks implements storage.Store.
func (s *Store) ListTasks(ctx context.Context, states []storage.State, limit int) ([]*storage.Task, error) {
	q := `
SELECT id, parent_id, project_id, task_type, backend, state, priority,
       envelope, run_id, pod_name, created_at, started_at, finished_at, pr_url,
       state_changed_at, reminded_at
FROM minos.tasks`
	args := []any{}
	if len(states) > 0 {
		strs := make([]string, len(states))
		for i, st := range states {
			strs[i] = string(st)
		}
		q += ` WHERE state = ANY($1)`
		args = append(args, strs)
	}
	q += ` ORDER BY created_at DESC`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT $%d`, len(args)+1)
		args = append(args, limit)
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("pgstore: list: %w", err)
	}
	defer rows.Close()

	var out []*storage.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TransitionTask implements storage.Store.
func (s *Store) TransitionTask(ctx context.Context, id uuid.UUID, to storage.State) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current storage.State
	if err := tx.QueryRow(ctx, `SELECT state FROM minos.tasks WHERE id = $1 FOR UPDATE`, id).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s", storage.ErrNotFound, id)
		}
		return fmt.Errorf("pgstore: lock row: %w", err)
	}
	if !validTransition(current, to) {
		return fmt.Errorf("%w: %s → %s", storage.ErrConflict, current, to)
	}

	now := time.Now().UTC()
	// Every transition stamps state_changed_at and clears reminded_at so
	// the hibernation sweeper starts fresh if the task re-enters
	// awaiting-review later.
	var q string
	args := []any{to, id, now}
	switch to {
	case storage.StateRunning:
		q = `UPDATE minos.tasks SET state = $1, started_at = $3, state_changed_at = $3, reminded_at = NULL WHERE id = $2`
	case storage.StateCompleted, storage.StateFailed:
		q = `UPDATE minos.tasks SET state = $1, finished_at = $3, state_changed_at = $3, reminded_at = NULL WHERE id = $2`
	default:
		q = `UPDATE minos.tasks SET state = $1, state_changed_at = $3, reminded_at = NULL WHERE id = $2`
	}
	if _, err := tx.Exec(ctx, q, args...); err != nil {
		return fmt.Errorf("pgstore: transition update: %w", err)
	}
	return tx.Commit(ctx)
}

// MarkTaskReminded implements storage.Store.
func (s *Store) MarkTaskReminded(ctx context.Context, id uuid.UUID) error {
	ct, err := s.pool.Exec(ctx,
		`UPDATE minos.tasks SET reminded_at = now() WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("pgstore: mark reminded: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", storage.ErrNotFound, id)
	}
	return nil
}

// SetTaskRun implements storage.Store.
func (s *Store) SetTaskRun(ctx context.Context, id, runID uuid.UUID, podName string) error {
	ct, err := s.pool.Exec(ctx,
		`UPDATE minos.tasks SET run_id = $1, pod_name = $2 WHERE id = $3`,
		runID, podName, id,
	)
	if err != nil {
		return fmt.Errorf("pgstore: set run: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", storage.ErrNotFound, id)
	}
	return nil
}

// SetTaskPR implements storage.Store.
func (s *Store) SetTaskPR(ctx context.Context, id uuid.UUID, prURL string) error {
	ct, err := s.pool.Exec(ctx,
		`UPDATE minos.tasks SET pr_url = $1 WHERE id = $2`,
		prURL, id,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: pr url %q already bound", storage.ErrConflict, prURL)
		}
		return fmt.Errorf("pgstore: set pr: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", storage.ErrNotFound, id)
	}
	return nil
}

// FindTaskByPRURL implements storage.Store.
func (s *Store) FindTaskByPRURL(ctx context.Context, prURL string) (*storage.Task, error) {
	const q = `
SELECT id, parent_id, project_id, task_type, backend, state, priority,
       envelope, run_id, pod_name, created_at, started_at, finished_at, pr_url,
       state_changed_at, reminded_at
FROM minos.tasks WHERE pr_url = $1`
	row := s.pool.QueryRow(ctx, q, prURL)
	task, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: pr %q", storage.ErrNotFound, prURL)
	}
	return task, err
}

func scanTask(r pgx.Row) (*storage.Task, error) {
	var (
		t              storage.Task
		envJSON        []byte
		parentID       *uuid.UUID
		runID          *uuid.UUID
		podName        *string
		startedAt      *time.Time
		finishedAt     *time.Time
		prURL          *string
		stateChangedAt time.Time
		remindedAt     *time.Time
	)
	if err := r.Scan(
		&t.ID, &parentID, &t.ProjectID, &t.TaskType, &t.Backend, &t.State, &t.Priority,
		&envJSON, &runID, &podName, &t.CreatedAt, &startedAt, &finishedAt, &prURL,
		&stateChangedAt, &remindedAt,
	); err != nil {
		return nil, err
	}
	if len(envJSON) > 0 {
		var e envelope.Envelope
		if err := json.Unmarshal(envJSON, &e); err != nil {
			return nil, fmt.Errorf("pgstore: unmarshal envelope: %w", err)
		}
		t.Envelope = &e
	}
	t.ParentID = parentID
	t.RunID = runID
	t.PodName = podName
	t.StartedAt = startedAt
	t.FinishedAt = finishedAt
	t.PRURL = prURL
	t.StateChangedAt = stateChangedAt
	t.RemindedAt = remindedAt
	return &t, nil
}

func validTransition(from, to storage.State) bool {
	switch from {
	case storage.StateQueued:
		return to == storage.StateRunning || to == storage.StateFailed
	case storage.StateRunning:
		return to == storage.StateAwaitingReview ||
			to == storage.StateCompleted ||
			to == storage.StateFailed
	case storage.StateAwaitingReview:
		return to == storage.StateRunning ||
			to == storage.StateCompleted ||
			to == storage.StateFailed
	default:
		return false
	}
}

// isUniqueViolation returns true when err is a Postgres unique-constraint
// violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	type pgCoder interface{ SQLState() string }
	var pce pgCoder
	if errors.As(err, &pce) {
		return pce.SQLState() == "23505"
	}
	return false
}
