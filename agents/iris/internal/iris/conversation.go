// Package iris is the long-running conversational pod per
// architecture.md §10. Slice 0 wires the read-only and commission
// surface against the Phase 1 Minos HTTP API and a Claude-backed
// inference path (Athena/Ollama lands when Athena is stood up — see
// docs/phase-2-plan.md §4 Slice 0 backend deviation).
package iris

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Turn is one entry in a conversation. Roles mirror Anthropic's Messages
// API: "user" is the operator, "assistant" is Iris's reply.
type Turn struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"ts"`
	// MsgSeq, when non-zero, is the Hermes pull-event sequence the turn
	// originated from. Iris uses it to deduplicate after pod restarts.
	MsgSeq uint64 `json:"msg_seq,omitempty"`
}

// Conversation is the persisted state for one (surface, thread, user)
// triple per architecture.md §10 Conversation State.
type Conversation struct {
	ID           uuid.UUID
	Surface      string
	ThreadRef    string
	UserIdentity string
	Summary      string
	Turns        []Turn
	UpdatedAt    time.Time
}

// ConversationStore persists Iris's conversation state. Backed by the
// shared Postgres iris.conversations table from migration 0008.
type ConversationStore struct {
	pool *pgxpool.Pool
}

// NewConversationStore wraps an existing pgxpool.Pool. Callers ensure the
// Minos migrations (which include 0008_iris_conversations.sql) have run.
func NewConversationStore(pool *pgxpool.Pool) *ConversationStore {
	return &ConversationStore{pool: pool}
}

// Get returns the conversation for the given key, or (nil, nil) when no
// row exists yet.
func (s *ConversationStore) Get(ctx context.Context, surface, threadRef, userIdentity string) (*Conversation, error) {
	const q = `
SELECT id, surface, thread_ref, user_identity, summary, turns, updated_at
FROM iris.conversations
WHERE surface = $1 AND thread_ref = $2 AND user_identity = $3`
	row := s.pool.QueryRow(ctx, q, surface, threadRef, userIdentity)
	var c Conversation
	var turnsRaw []byte
	if err := row.Scan(&c.ID, &c.Surface, &c.ThreadRef, &c.UserIdentity, &c.Summary, &turnsRaw, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("iris.conversations get: %w", err)
	}
	if len(turnsRaw) > 0 {
		if err := json.Unmarshal(turnsRaw, &c.Turns); err != nil {
			return nil, fmt.Errorf("iris.conversations decode turns: %w", err)
		}
	}
	return &c, nil
}

// Upsert persists the conversation, creating the row on first contact and
// updating turns + summary on subsequent calls.
func (s *ConversationStore) Upsert(ctx context.Context, c *Conversation) error {
	turnsRaw, err := json.Marshal(c.Turns)
	if err != nil {
		return fmt.Errorf("iris.conversations encode turns: %w", err)
	}
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	const q = `
INSERT INTO iris.conversations (id, surface, thread_ref, user_identity, summary, turns)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (surface, thread_ref, user_identity)
DO UPDATE SET summary = EXCLUDED.summary, turns = EXCLUDED.turns, updated_at = now()`
	if _, err := s.pool.Exec(ctx, q, c.ID, c.Surface, c.ThreadRef, c.UserIdentity, c.Summary, turnsRaw); err != nil {
		return fmt.Errorf("iris.conversations upsert: %w", err)
	}
	return nil
}

// AppendTurn loads (or creates) the conversation and appends one turn.
// Returns the resulting Conversation snapshot. Convenience for the common
// "new message in, new turn out" path.
func (s *ConversationStore) AppendTurn(ctx context.Context, surface, threadRef, userIdentity string, turn Turn) (*Conversation, error) {
	c, err := s.Get(ctx, surface, threadRef, userIdentity)
	if err != nil {
		return nil, err
	}
	if c == nil {
		c = &Conversation{
			Surface:      surface,
			ThreadRef:    threadRef,
			UserIdentity: userIdentity,
		}
	}
	c.Turns = append(c.Turns, turn)
	if err := s.Upsert(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// AlreadyHandled reports whether the conversation already contains a turn
// with the given message sequence — Iris's idempotency check after a
// restart that re-reads buffered events.
func (s *ConversationStore) AlreadyHandled(ctx context.Context, surface, threadRef, userIdentity string, msgSeq uint64) (bool, error) {
	c, err := s.Get(ctx, surface, threadRef, userIdentity)
	if err != nil || c == nil {
		return false, err
	}
	for _, t := range c.Turns {
		if t.MsgSeq == msgSeq {
			return true, nil
		}
	}
	return false, nil
}
