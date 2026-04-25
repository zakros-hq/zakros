-- +goose Up
-- The four Zakros schemas per architecture.md §6 Recovery and Reconciliation.
-- pgvector is created here so Mnemosyne's Slice C migrations can assume it
-- exists; no pgvector columns are created yet.
CREATE SCHEMA IF NOT EXISTS minos;
CREATE SCHEMA IF NOT EXISTS argus;
CREATE SCHEMA IF NOT EXISTS mnemosyne;
CREATE SCHEMA IF NOT EXISTS iris;
CREATE EXTENSION IF NOT EXISTS vector;

-- +goose Down
DROP SCHEMA IF EXISTS iris CASCADE;
DROP SCHEMA IF EXISTS mnemosyne CASCADE;
DROP SCHEMA IF EXISTS argus CASCADE;
DROP SCHEMA IF EXISTS minos CASCADE;
DROP EXTENSION IF EXISTS vector;
