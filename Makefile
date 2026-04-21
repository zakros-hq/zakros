.PHONY: all build test vet lint fmt tidy clean dev-postgres dev-postgres-stop dev-k3d dev-k3d-stop plugin-claude-code plugin-shellcheck sidecar-argus tf-fmt tf-validate tf-plan tf-apply tf-inventory

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

# Plugin images

PLUGIN_IMAGE_TAG ?= local
PLUGIN_IMAGE_REPO ?= ghcr.io/goodolclint/daedalus-claude-code

plugin-claude-code:
	docker build -t $(PLUGIN_IMAGE_REPO):$(PLUGIN_IMAGE_TAG) ./agents/claude-code

SIDECAR_ARGUS_REPO ?= ghcr.io/goodolclint/daedalus-argus-sidecar

sidecar-argus:
	docker build -f agents/sidecar/argus/Dockerfile -t $(SIDECAR_ARGUS_REPO):$(PLUGIN_IMAGE_TAG) .

plugin-shellcheck:
	bash -n agents/claude-code/entrypoint.sh
	@command -v shellcheck >/dev/null && shellcheck agents/claude-code/entrypoint.sh || echo "shellcheck not installed; only bash -n ran"

# Terraform

tf-fmt:
	cd terraform && terraform fmt -recursive

tf-validate:
	cd terraform && terraform init -backend=false -input=false && terraform validate

tf-plan:
	cd terraform && terraform plan

tf-apply:
	cd terraform && terraform apply

tf-inventory:
	cd terraform && terraform output -raw ansible_inventory_yaml > ../inventory.yaml
	@echo "Wrote inventory.yaml"
