package core

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/zakros-hq/zakros/pkg/audit"
	"github.com/zakros-hq/zakros/pkg/jwt"
)

// irisTokenTTL is how long the long-lived JWT minted for the Iris pod
// remains valid. Iris is a long-running pod replaced on rolling
// updates; a year balances "operators don't think about it often"
// against "key rotation invalidates outstanding tokens, so don't
// hold them forever." Reduce when rotation cadence increases.
const irisTokenTTL = 365 * 24 * time.Hour

// irisAnthropicModels enumerates the Anthropic models Iris's JWT is
// allowed to invoke through Apollo. Slice H2a hardcodes the set;
// future config-driven enumeration lifts this when more than one
// model is regularly in play. Operator note: this list MUST be a
// subset of apollo.json's `allowed_anthropic_models` — Apollo refuses
// any model not in its allowlist before the scope check runs, so a
// mismatch surfaces as `unknown model` from the broker.
var irisAnthropicModels = []string{
	"claude-sonnet-4-5",
	"claude-opus-4-5",
}

// handleMintIrisToken returns a Minos-signed JWT scoped for the Iris
// pod's needs: state queries (audience=minos), Hermes pull/post
// (audience=hermes), Mnemosyne lookup (audience=mnemosyne), and —
// since Slice H2a — Anthropic inference via Apollo (audience=apollo,
// per-model scopes). The operator runs `minosctl mint-iris-token`,
// pastes the JWT into deploy/secrets.json under minos/iris-token, and
// re-runs iris-install.
//
// Subject is fixed to "iris" (no per-pod uniqueness; one Iris pod per
// deployment). Replay protection isn't applied to Iris's routes since
// the underlying handlers are idempotent; the long TTL is the cost the
// posture accepts.
func (s *Server) handleMintIrisToken(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	apolloScopes := make([]string, 0, len(irisAnthropicModels))
	for _, m := range irisAnthropicModels {
		apolloScopes = append(apolloScopes, "apollo.anthropic."+m)
	}
	claims := jwt.Claims{
		Subject:  "iris",
		Issuer:   "minos",
		Audience: []string{"minos", "hermes", "mnemosyne", "apollo"},
		IssuedAt: now,
		Expires:  now.Add(irisTokenTTL),
		JTI:      uuid.NewString(),
		McpScopes: map[string][]string{
			"minos":     {"query_state"},
			"hermes":    {"events.next", "post_as_iris"},
			"mnemosyne": {"memory.lookup"},
			"apollo":    apolloScopes,
		},
	}
	tok, err := jwt.Sign(s.signingKey, claims)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sign jwt: "+err.Error())
		return
	}
	s.audit.Emit(audit.Event{
		Category: "admin",
		Outcome:  "iris-token-minted",
		Fields:   map[string]string{"jti": claims.JTI, "ttl": irisTokenTTL.String()},
	})
	writeJSON(w, http.StatusOK, map[string]string{"token": tok})
}
