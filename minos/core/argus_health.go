package core

import (
	"context"
	"net/http"
	"time"

	"github.com/zakros-hq/zakros/pkg/audit"
)

// argusHealthInterval is how often Minos polls the extracted Argus's
// /healthz. 30s matches the symmetric Argus→Minos check, so a single
// transition surfaces in both audit streams within roughly the same
// window.
const argusHealthInterval = 30 * time.Second

// runArgusHealthMonitor polls Argus's /healthz on argusHealthInterval
// cadence and audits transitions. The Phase 3 Asclepius service
// replaces this hand-rolled loop; for Slice J it's enough that a
// stalled Argus is observable from Minos's audit stream.
func (s *Server) runArgusHealthMonitor(ctx context.Context) {
	ticker := time.NewTicker(argusHealthInterval)
	defer ticker.Stop()
	client := &http.Client{Timeout: 5 * time.Second}
	healthy := true
	check := func() {
		req, err := http.NewRequestWithContext(ctx, "GET", s.cfg.ArgusURL+"/healthz", nil)
		if err != nil {
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			if healthy {
				s.audit.Emit(audit.Event{
					Category: "minos", Outcome: "argus-unhealthy",
					Message: err.Error(),
				})
			}
			healthy = false
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			if healthy {
				s.audit.Emit(audit.Event{
					Category: "minos", Outcome: "argus-unhealthy",
					Fields: map[string]string{"status": resp.Status},
				})
			}
			healthy = false
			return
		}
		if !healthy {
			s.audit.Emit(audit.Event{
				Category: "minos", Outcome: "argus-recovered",
			})
		}
		healthy = true
	}
	check()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}
