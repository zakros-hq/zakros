-- +goose Up
-- Per-task Argus state, serialized as JSON. Phase 1 simple persistence:
-- one row per tracked pod, full State blob. Phase 2 may decompose into
-- columns once the rules engine surface stabilises.
CREATE TABLE argus.task_states (
    task_id    uuid PRIMARY KEY,
    state      jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX argus_task_states_updated_idx ON argus.task_states(updated_at);

-- +goose Down
DROP TABLE IF EXISTS argus.task_states;
