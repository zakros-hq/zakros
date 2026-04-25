// Package storage defines the persistence interface Minos's core depends
// on. Two implementations ship in Phase 1: pgstore backed by the shared
// Postgres LXC (production) and memstore for tests and local dev. Any
// implementation meeting this interface is a valid substitute.
package storage

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/zakros-hq/zakros/pkg/envelope"
)

// ErrNotFound is returned when a task lookup fails.
var ErrNotFound = errors.New("task not found")

// ErrConflict is returned when a state transition would violate the state
// machine (e.g. finishing a task already marked completed).
var ErrConflict = errors.New("task state conflict")

// State enumerates the Phase 1 task states.
type State string

const (
	StateQueued          State = "queued"
	StateRunning         State = "running"
	StateAwaitingReview  State = "awaiting-review"
	StateCompleted       State = "completed"
	StateFailed          State = "failed"
)

// Task is the persisted record of one commissioned task.
type Task struct {
	ID             uuid.UUID
	ParentID       *uuid.UUID
	ProjectID      string
	TaskType       envelope.TaskType
	Backend        string
	State          State
	Priority       int16
	Envelope       *envelope.Envelope
	RunID          *uuid.UUID
	PodName        *string
	PRURL          *string
	CreatedAt      time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
	StateChangedAt time.Time
	RemindedAt     *time.Time
}

// Store is the contract every storage implementation must satisfy for
// Slice A. Slice C extends it with Mnemosyne lookups; Slice D with Argus
// state queries.
type Store interface {
	// InsertTask persists a new task in StateQueued. It returns ErrConflict
	// if a task with the same ID already exists.
	InsertTask(ctx context.Context, t *Task) error

	// GetTask returns the task identified by id or ErrNotFound.
	GetTask(ctx context.Context, id uuid.UUID) (*Task, error)

	// ListTasks returns tasks in descending CreatedAt order, optionally
	// filtered by state. A nil states slice returns all tasks.
	ListTasks(ctx context.Context, states []State, limit int) ([]*Task, error)

	// TransitionTask atomically updates a task's state plus the associated
	// timestamps. valid transitions per the Phase 1 state machine:
	//   queued          → running          (sets StartedAt)
	//   queued          → failed           (sets FinishedAt)  // dispatch-time failures
	//   running         → awaiting-review  (pod exited after PR open)
	//   running         → completed        (sets FinishedAt)  // PR merged while running
	//   running         → failed           (sets FinishedAt)
	//   awaiting-review → running          (respawn; clears StartedAt? no — keep first-ever start)
	//   awaiting-review → completed        (sets FinishedAt)  // PR merged during hibernation
	//   awaiting-review → failed           (sets FinishedAt)  // PR closed unmerged during hibernation
	// Any other transition returns ErrConflict.
	TransitionTask(ctx context.Context, id uuid.UUID, to State) error

	// SetTaskRun records the k3s pod name and generated run ID once a pod
	// has been successfully spawned for this task.
	SetTaskRun(ctx context.Context, id uuid.UUID, runID uuid.UUID, podName string) error

	// SetTaskPR records the PR URL the pod opened. Called from the pod's
	// POST /tasks/{id}/pr callback. Returns ErrConflict if the URL is
	// already bound to a different task.
	SetTaskPR(ctx context.Context, id uuid.UUID, prURL string) error

	// FindTaskByPRURL returns the task whose PR URL matches exactly, or
	// ErrNotFound. Used by the GitHub webhook handler to resolve
	// pull_request events back to the owning task.
	FindTaskByPRURL(ctx context.Context, prURL string) (*Task, error)

	// MarkTaskReminded stamps RemindedAt on a task so the hibernation
	// sweeper doesn't double-remind.
	MarkTaskReminded(ctx context.Context, id uuid.UUID) error
}
