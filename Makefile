.PHONY: help all build test vet lint fmt tidy clean \
        dev-postgres dev-postgres-stop dev-k3d dev-k3d-stop \
        plugin-claude-code plugin-shellcheck sidecar-argus \
        tf-fmt tf-validate tf-plan tf-apply tf-apply-firewall tf-destroy tf-inventory

GO := go
LINTER := golangci-lint
BIN_DIR := bin

# Default target prints help; run `make all` to build+test.
.DEFAULT_GOAL := help

help: ## List available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-24s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# -- Go ---------------------------------------------------------------

all: build test ## Build binaries and run the test suite

build: ## Build the Minos daemon and minosctl CLI into bin/
	$(GO) build -o $(BIN_DIR)/minos ./cmd/minos
	$(GO) build -o $(BIN_DIR)/minosctl ./cmd/minosctl

test: ## Run every Go test (pgstore integration tests skip unless ZAKROS_TEST_POSTGRES_DSN is set)
	$(GO) test ./...

vet: ## go vet the whole tree
	$(GO) vet ./...

lint: ## Run golangci-lint (must be installed separately)
	$(LINTER) run ./...

fmt: ## gofmt the whole tree
	$(GO) fmt ./...

tidy: ## go mod tidy
	$(GO) mod tidy

clean: ## Remove build artifacts in bin/
	rm -rf $(BIN_DIR)

# -- Local dev substrates (docs/phase-1-plan.md §4) -------------------

dev-postgres: ## Start a local Postgres with pgvector for integration tests
	docker run -d --rm --name zakros-dev-postgres \
		-e POSTGRES_PASSWORD=devpass \
		-e POSTGRES_USER=zakros \
		-e POSTGRES_DB=zakros \
		-p 5432:5432 \
		pgvector/pgvector:pg17

dev-postgres-stop: ## Stop and remove the local Postgres container
	docker stop zakros-dev-postgres

dev-k3d: ## Create a local k3d cluster for dispatcher integration
	k3d cluster create zakros-dev --agents 0 --no-lb

dev-k3d-stop: ## Delete the local k3d cluster
	k3d cluster delete zakros-dev

# -- Container images -------------------------------------------------

PLUGIN_IMAGE_TAG ?= local
PLUGIN_IMAGE_REPO ?= ghcr.io/zakros-hq/zakros-claude-code
SIDECAR_ARGUS_REPO ?= ghcr.io/zakros-hq/zakros-argus-sidecar

plugin-claude-code: ## Build the Claude Code worker pod image
	docker build -t $(PLUGIN_IMAGE_REPO):$(PLUGIN_IMAGE_TAG) ./agents/claude-code

sidecar-argus: ## Build the Argus heartbeat sidecar image
	docker build -f agents/sidecar/argus/Dockerfile -t $(SIDECAR_ARGUS_REPO):$(PLUGIN_IMAGE_TAG) .

plugin-shellcheck: ## Syntax-check the Claude Code entrypoint (shellcheck if installed, bash -n otherwise)
	bash -n agents/claude-code/entrypoint.sh
	@command -v shellcheck >/dev/null && shellcheck agents/claude-code/entrypoint.sh || echo "shellcheck not installed; only bash -n ran"

# -- Terraform --------------------------------------------------------

tf-fmt: ## terraform fmt -recursive
	cd terraform && terraform fmt -recursive

tf-validate: ## terraform init (no backend) + validate
	cd terraform && terraform init -backend=false -input=false && terraform validate

tf-plan: ## terraform plan (requires TF_VAR_proxmox_api_token etc. in env)
	cd terraform && terraform plan

tf-apply: ## terraform apply everything (run tf-apply-firewall first on a fresh build so OPNsense routes before guests boot)
	cd terraform && terraform apply

tf-apply-firewall: ## Phase 1 of a fresh apply — create SDN + OPNsense only, then wait for bootstrap before tf-apply
	cd terraform && terraform apply -target=module.sdn -target=module.firewall

tf-destroy: ## terraform destroy — tears down every Zakros guest + SDN on Crete
	cd terraform && terraform destroy

tf-inventory: ## Dump the Terraform-generated guest inventory to ./inventory.yaml
	cd terraform && terraform output -raw guests_yaml > ../inventory.yaml
	@echo "Wrote inventory.yaml"
