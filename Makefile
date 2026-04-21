.PHONY: all build test vet lint fmt tidy clean dev-postgres dev-postgres-stop dev-k3d dev-k3d-stop

GO := go
LINTER := golangci-lint
BIN_DIR := bin

all: build test

build:
	$(GO) build -o $(BIN_DIR)/minos ./cmd/minos
	$(GO) build -o $(BIN_DIR)/minosctl ./cmd/minosctl

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

lint:
	$(LINTER) run ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BIN_DIR)

# Local dev substrates (see docs/phase-1-plan.md §4 Local dev substrates)

dev-postgres:
	docker run -d --rm --name daedalus-dev-postgres \
		-e POSTGRES_PASSWORD=devpass \
		-e POSTGRES_USER=daedalus \
		-e POSTGRES_DB=daedalus \
		-p 5432:5432 \
		pgvector/pgvector:pg17

dev-postgres-stop:
	docker stop daedalus-dev-postgres

dev-k3d:
	k3d cluster create daedalus-dev --agents 0 --no-lb

dev-k3d-stop:
	k3d cluster delete daedalus-dev
