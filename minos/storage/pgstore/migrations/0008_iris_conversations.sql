-- +goose Up
-- Iris conversation state per architecture.md §10 Conversation State.
-- Keyed by (surface, thread_ref, user_identity); holds recent turns plus
-- a running summary so context survives Iris pod replacement.
--
-- turns is an append-mostly JSON array of {role, content, ts, msg_id?}.
-- summary is a rolling LLM-produced digest used when turns exceed the
-- working window. Phase 1 keeps both on the same row; if turns grows
-- unbounded a future migration can break it out into a child table.
CREATE TABLE iris.conversations (
    id            uuid PRIMARY KEY,
    surface       text        NOT NULL,
    thread_ref    text        NOT NULL,
    user_identity text        NOT NULL,
    summary       text        NOT NULL DEFAULT '',
    turns         jsonb       NOT NULL DEFAULT '[]'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (surface, thread_ref, user_identity)
);

CREATE INDEX iris_conversations_updated_idx ON iris.conversations(updated_at);

-- +goose Down
DROP TABLE IF EXISTS iris.conversations;
