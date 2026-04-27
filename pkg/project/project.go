// Package project defines the registry value type per architecture.md
// §6 Project Registry. Phase 2 single-project = one row; Phase 3
// multi-project adds rows + per-project Hermes/egress/identity scoping.
//
// Shape mirrors what minos/core consumed as ProjectConfig in Phase 1
// minus the JSON-tag annotations specific to the on-disk config file —
// the registry is the runtime source of truth, the config-file shape
// is bootstrap-only.
package project

import (
	"errors"

	"github.com/zakros-hq/zakros/pkg/envelope"
)

// Project is one registered project's full runtime configuration.
type Project struct {
	ID                       string
	Name                     string
	Backend                  string
	PluginImage              string
	ArgusSidecarImage        string
	AgentMentionHandle       string
	DefaultWorkspaceSize     envelope.WorkspaceSize
	DefaultBaseBranch        string
	DefaultRepoURL           string
	BranchProtectionRequired bool
	MnemosyneRetentionDays   int
	DefaultBudget            envelope.Budget
	Communication            envelope.Communication
	ThreadParent             string
	Capabilities             Capabilities
}

// Capabilities are the per-project capability defaults composed into
// every task envelope this project commissions.
type Capabilities struct {
	InjectedCredentials []envelope.InjectedCredential
	HecateCredentials   []envelope.HecateCredential
	McpEndpoints        []envelope.McpEndpoint
}

// Errors callers can switch on.
var (
	// ErrNotFound — project lookup miss.
	ErrNotFound = errors.New("project: not found")
	// ErrAlreadyExists — duplicate project ID on insert.
	ErrAlreadyExists = errors.New("project: already exists")
)
