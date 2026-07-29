package core

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/zakros-hq/zakros/pkg/audit"
	"github.com/zakros-hq/zakros/pkg/jwt"
)

// apolloTokenTTL matches Iris's TTL: long-lived service JWT, rotated by
// rotating Minos's signing key. Phase 2 H2a single-tenant simplification.
const apolloTokenTTL = 365 * 24 * time.Hour

// handleMintApolloToken returns Apollo's long-lived service JWT. Apollo
// uses it to fetch the upstream provider credentials (Anthropic API key,
// future OpenAI key, etc.) from Hecate at startup. The operator runs
// `minosctl mint-apollo-token`, pastes into deploy/secrets.json under
// minos/apollo-token, and re-runs deploy/apollo-install.sh.
//
// Subject is fixed to "apollo" (one Apollo instance per deployment).
// Audience and scopes mirror what Apollo needs to call: just Hecate,
// scoped to the per-provider credential refs Apollo's plugins know
// about. New provider plugins extend this scope set.
func (s *Server) handleMintApolloToken(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	claims := jwt.Claims{
		Subject:  "apollo",
		Issuer:   "minos",
		Audience: []string{"hecate"},
		IssuedAt: now,
		Expires:  now.Add(apolloTokenTTL),
		JTI:      uuid.NewString(),
		McpScopes: map[string][]string{
			"hecate": {
				"credentials.fetch:anthropic-api-key",
			},
		},
	}
	tok, err := jwt.Sign(s.signingKey, claims)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sign jwt: "+err.Error())
		return
	}
	s.audit.Emit(audit.Event{
		Category: "admin",
		Outcome:  "apollo-token-minted",
		Fields:   map[string]string{"jti": claims.JTI, "ttl": apolloTokenTTL.String()},
	})
	writeJSON(w, http.StatusOK, map[string]string{"token": tok})
}
