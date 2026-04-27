package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/zakros-hq/zakros/minos/identity"
	"github.com/zakros-hq/zakros/minos/project"
	idn "github.com/zakros-hq/zakros/pkg/identity"
	prj "github.com/zakros-hq/zakros/pkg/project"
)

// bootstrapIdentities seeds the identity registry from the config-file
// admins + system_identities blocks per architecture.md §6 Command
// Intake and Pairing. Idempotent: re-running with the same config is
// a no-op; adding a new admin tuple to the config inserts it; removing
// one from the config does NOT revoke (that's a deliberate runtime
// operator action, not a config drift).
func bootstrapIdentities(ctx context.Context, store identity.Store, admins []AdminIdentity, systems []SystemIdentity) error {
	for _, a := range admins {
		if a.Surface == "" || a.SurfaceID == "" {
			return errors.New("bootstrap: admin requires surface + surface_id")
		}
		_, err := store.LookupBySurface(ctx, a.Surface, a.SurfaceID)
		switch {
		case err == nil:
			// Already registered — leave the row alone (operator may
			// have demoted/altered it intentionally via /minos commands).
			continue
		case errors.Is(err, idn.ErrNotFound):
			if err := store.Insert(ctx, &idn.Identity{
				Surface:   a.Surface,
				SurfaceID: a.SurfaceID,
				Role:      idn.RoleAdmin,
				Status:    idn.StatusActive,
			}); err != nil {
				return fmt.Errorf("bootstrap: insert admin %s/%s: %w", a.Surface, a.SurfaceID, err)
			}
		default:
			return fmt.Errorf("bootstrap: lookup admin %s/%s: %w", a.Surface, a.SurfaceID, err)
		}
	}
	for _, sys := range systems {
		if sys.Surface == "" || sys.SurfaceID == "" {
			return errors.New("bootstrap: system identity requires surface + surface_id")
		}
		_, err := store.LookupBySurface(ctx, sys.Surface, sys.SurfaceID)
		switch {
		case err == nil:
			continue
		case errors.Is(err, idn.ErrNotFound):
			if err := store.Insert(ctx, &idn.Identity{
				Surface:   sys.Surface,
				SurfaceID: sys.SurfaceID,
				Role:      idn.RoleSystem,
				Status:    idn.StatusActive,
			}); err != nil {
				return fmt.Errorf("bootstrap: insert system %s/%s: %w", sys.Surface, sys.SurfaceID, err)
			}
		default:
			return fmt.Errorf("bootstrap: lookup system %s/%s: %w", sys.Surface, sys.SurfaceID, err)
		}
	}
	return nil
}

// bootstrapProject upserts the singleton project from the on-disk
// config so the registry reflects whatever the operator currently
// has in deploy/config.json. Phase 2 single-project = one row;
// changes to the project block on every minos restart land here.
func bootstrapProject(ctx context.Context, store project.Store, cfg ProjectConfig) error {
	if cfg.ID == "" {
		return errors.New("bootstrap: project.id required")
	}
	p := projectFromConfig(cfg)
	return store.Upsert(ctx, p)
}

// projectFromConfig converts the on-disk ProjectConfig shape into the
// pkg/project value type the registry stores. Name defaults to ID
// when not specified — Phase 1 ProjectConfig had no Name field.
func projectFromConfig(cfg ProjectConfig) *prj.Project {
	name := cfg.ID
	return &prj.Project{
		ID:                       cfg.ID,
		Name:                     name,
		Backend:                  cfg.Backend,
		PluginImage:              cfg.PluginImage,
		ArgusSidecarImage:        cfg.ArgusSidecarImage,
		AgentMentionHandle:       cfg.AgentMentionHandle,
		DefaultWorkspaceSize:     cfg.DefaultWorkspaceSize,
		DefaultBaseBranch:        cfg.DefaultBaseBranch,
		DefaultRepoURL:           cfg.DefaultRepoURL,
		BranchProtectionRequired: false, // operator-set; default off until enforcement lands
		MnemosyneRetentionDays:   0,
		DefaultBudget:            cfg.DefaultBudget,
		Communication:            cfg.Communication,
		ThreadParent:             cfg.ThreadParent,
		Capabilities: prj.Capabilities{
			InjectedCredentials: cfg.Capabilities.InjectedCredentials,
			HecateCredentials:   cfg.Capabilities.HecateCredentials,
			McpEndpoints:        cfg.Capabilities.McpEndpoints,
		},
	}
}
