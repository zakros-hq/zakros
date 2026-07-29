package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/zakros-hq/zakros/pkg/audit"
	"github.com/zakros-hq/zakros/pkg/brokerauth"
	"github.com/zakros-hq/zakros/pkg/jwt"
)

// scopePrefix is the JWT scope namespace Apollo accepts. Per
// architecture.md §6 MCP Broker Authentication the scope shape is
// `apollo.<provider>.<model>`. Apollo extracts the provider+model
// from the request body and checks the JWT's apollo scope set
// contains the resulting string.
const scopePrefix = "apollo."

// maxRequestBytes caps the inbound /v1/messages body. The Anthropic
// API itself enforces upstream limits, but Apollo reads the full body
// before forwarding (to extract the model and audit) so we cap to
// keep memory bounded.
const maxRequestBytes = 1 << 20 // 1 MiB

// server bundles Apollo's HTTP runtime.
type server struct {
	logger   *slog.Logger
	audit    audit.Emitter
	verifier *brokerauth.Verifier
	registry *providerRegistry
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	// Apollo's main surface mirrors the upstream Anthropic Messages
	// API path so the claude CLI's ANTHROPIC_BASE_URL just-works as
	// a drop-in. Phase 2 H2a is non-streaming only; streaming lands
	// when Iris/worker-pod usage demands it.
	mux.HandleFunc("POST /v1/messages", s.dispatchMessages)
	return mux
}

func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// dispatchMessages is the per-call JWT + scope check, body parse, and
// upstream forward. Scope is dynamic (depends on the model), so we
// inline the brokerauth check rather than wrapping with Verifier.Require.
func (s *server) dispatchMessages(w http.ResponseWriter, r *http.Request) {
	// Read the body up-front because we need the model to compute the
	// scope, and forwarding wants the same bytes. The cap is
	// defensive — Anthropic's own request size limit is upstream.
	bodyBytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	var head struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(bodyBytes, &head); err != nil {
		writeError(w, http.StatusBadRequest, "parse body: "+err.Error())
		return
	}
	if head.Model == "" {
		writeError(w, http.StatusBadRequest, "model required")
		return
	}

	// Provider routing by model name. Phase 2 H2a registers the
	// Anthropic provider only; the lookup is the fan-out point that
	// future OpenAI/Google plugins extend.
	provider := s.registry.providerFor(head.Model)
	if provider == nil {
		writeError(w, http.StatusBadRequest, "unknown model: "+head.Model)
		return
	}

	expectedScope := scopePrefix + provider.Name() + "." + head.Model

	bearer := bearerFromHeader(r.Header.Get("Authorization"))
	if bearer == "" {
		s.deny(r, "missing bearer", expectedScope, "")
		writeError(w, http.StatusUnauthorized, "missing or malformed bearer")
		return
	}
	claims, err := jwt.Verify(s.verifier.PublicKey, bearer)
	if err != nil {
		s.deny(r, "invalid signature", expectedScope, "")
		writeError(w, http.StatusUnauthorized, "invalid bearer")
		return
	}
	if !claims.HasAudience("apollo") {
		s.deny(r, "audience mismatch", expectedScope, claims.Subject)
		writeError(w, http.StatusForbidden, "audience mismatch")
		return
	}
	if !claims.HasScope("apollo", expectedScope) {
		s.deny(r, "scope denied", expectedScope, claims.Subject)
		writeError(w, http.StatusForbidden, "scope denied")
		return
	}

	resp, err := provider.Forward(r.Context(), bodyBytes)
	if err != nil {
		s.audit.Emit(audit.Event{
			Category: "apollo", Outcome: "upstream-error",
			Message: err.Error(),
			Fields:  map[string]string{"model": head.Model, "sub": claims.Subject},
		})
		writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		return
	}

	// Apollo logs every successful call with the usage counts the
	// upstream returned. H2b promotes this to a push event into Argus
	// for cross-broker rate-limit enforcement.
	s.audit.Emit(audit.Event{
		Category: "apollo", Outcome: "messages",
		Fields: map[string]string{
			"model":     head.Model,
			"provider":  provider.Name(),
			"sub":       claims.Subject,
			"jti":       claims.JTI,
			"tokens_in":  itoa(resp.TokensIn),
			"tokens_out": itoa(resp.TokensOut),
			"status":    itoa(resp.Status),
		},
	})

	// Pass upstream response headers through unchanged so the caller
	// gets the same anthropic-ratelimit-* observability the bare API
	// provides. Header relay is also what H2b will consume to derive
	// non-forgeable usage events.
	for k, vs := range resp.Headers {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body)
}

func (s *server) deny(r *http.Request, reason, scope, sub string) {
	fields := map[string]string{
		"path":   r.URL.Path,
		"scope":  scope,
		"reason": reason,
	}
	if sub != "" {
		fields["sub"] = sub
	}
	s.audit.Emit(audit.Event{
		Category: "apollo",
		Outcome:  "denied:" + reason,
		Fields:   fields,
	})
}

func bearerFromHeader(h string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func itoa(n int) string {
	// strconv.Itoa pulled inline to keep imports tight; this is the
	// only numeric conversion in the package.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
