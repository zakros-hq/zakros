// Package memstore is the in-memory mnemosyne/core.Store for tests and
// local development without a Postgres dependency.
package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	mcore "github.com/zakros-hq/zakros/mnemosyne/core"
)

// Store is the in-memory implementation.
type Store struct {
	mu      sync.RWMutex
	records []*mcore.RunRecord

	// MaxContextRuns caps how many prior summaries feed into a GetContext
	// assembly. Zero means unlimited.
	MaxContextRuns int
}

// New returns a Store with sensible defaults.
func New() *Store { return &Store{MaxContextRuns: 5} }

// StoreRun satisfies mnemosyne/core.Store.
func (s *Store) StoreRun(_ context.Context, rec *mcore.RunRecord) error {
	if rec == nil {
		return fmt.Errorf("memstore: nil record")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := *rec
	if clone.ID == uuid.Nil {
		clone.ID = uuid.New()
	}
	if clone.CreatedAt.IsZero() {
		clone.CreatedAt = time.Now().UTC()
	}
	s.records = append(s.records, &clone)
	return nil
}

// GetContext satisfies mnemosyne/core.Store. Simple assembly: newest-first
// concatenation of prior-run summaries for the same project + task type.
func (s *Store) GetContext(_ context.Context, projectID, taskType string) (*mcore.Context, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	matches := make([]*mcore.RunRecord, 0)
	for _, r := range s.records {
		if r.ProjectID != projectID {
			continue
		}
		if taskType != "" && r.TaskType != taskType {
			continue
		}
		matches = append(matches, r)
	}
	if len(matches) == 0 {
		return nil, nil
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})
	if s.MaxContextRuns > 0 && len(matches) > s.MaxContextRuns {
		matches = matches[:s.MaxContextRuns]
	}
	return &mcore.Context{
		Ref:       fmt.Sprintf("mem:%s:%d", projectID, len(matches)),
		Body:      assemble(matches),
		PriorRuns: len(matches),
	}, nil
}

// GetRunsForTask satisfies mnemosyne/core.Store.
func (s *Store) GetRunsForTask(_ context.Context, taskID uuid.UUID) ([]*mcore.RunRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*mcore.RunRecord, 0)
	for _, r := range s.records {
		if r.TaskID == taskID {
			clone := *r
			out = append(out, &clone)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// assemble is a Phase 1 primitive: concatenate the summaries, newest
// first, with simple framing. Phase 2 replaces this with pgvector semantic
// lookup and trust-marker preservation.
func assemble(records []*mcore.RunRecord) string {
	var sb stringBuilder
	sb.WriteString("Prior-run context (newest first):\n")
	for i, r := range records {
		sb.WriteString(fmt.Sprintf("\n[%d] run=%s outcome=%s at=%s\n",
			i+1, r.RunID.String()[:8], r.Outcome, r.CreatedAt.Format(time.RFC3339)))
		if r.Summary != "" {
			sb.WriteString(r.Summary)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// stringBuilder is a minimal wrapper so the helper stays dep-free.
type stringBuilder struct {
	b []byte
}

func (s *stringBuilder) WriteString(v string) { s.b = append(s.b, v...) }
func (s *stringBuilder) String() string       { return string(s.b) }
