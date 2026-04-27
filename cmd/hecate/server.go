package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/zakros-hq/zakros/pkg/audit"
	"github.com/zakros-hq/zakros/pkg/brokerauth"
	"github.com/zakros-hq/zakros/pkg/jwt"
)

// scopePrefix is the JWT scope namespace Hecate accepts. Per
// architecture.md §6 MCP Broker Authentication the scope shape is
// `credentials.fetch:<ref>`; the broker checks scope match by
// matching `<ref>` from the URL path against the JWT's scope value
// suffix.
const scopePrefix = "credentials.fetch:"

// server bundles Hecate's HTTP runtime.
type server struct {
	logger   *slog.Logger
	audit    audit.Emitter
	verifier *brokerauth.Verifier
	vault    *vaultClient
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	// `GET /credentials/fetch/{ref}` — JWT-gated fetch. Scope encoded
	// into the path because brokerauth.Verifier.Require takes a single
	// scope per route; we wrap with a custom dispatcher that pulls the
	// ref out and verifies the scope manually.
	mux.HandleFunc("GET /credentials/fetch/{ref}", s.dispatchFetch)
	return mux
}

func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// dispatchFetch verifies the JWT manually (audience=hecate, scope
// `credentials.fetch:<ref>` matching the URL path), then reads from
// Vault and returns the value. Plain-bearer custom dispatch because
// the scope is per-request, not per-route.
func (s *server) dispatchFetch(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	if ref == "" {
		writeError(w, http.StatusBadRequest, "ref path segment required")
		return
	}

	// Re-implement the brokerauth check inline because scope is
	// dynamic. Same semantics as Verifier.Require but with the scope
	// computed from the request.
	expectedScope := scopePrefix + ref
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
	if !claims.HasAudience("hecate") {
		s.deny(r, "audience mismatch", expectedScope, claims.Subject)
		writeError(w, http.StatusForbidden, "audience mismatch")
		return
	}
	if !claims.HasScope("hecate", expectedScope) {
		s.deny(r, "scope denied", expectedScope, claims.Subject)
		writeError(w, http.StatusForbidden, "scope denied")
		return
	}

	val, err := s.vault.readKV(r.Context(), ref)
	if err != nil {
		if errors.Is(err, ErrVaultNotFound) {
			s.deny(r, "vault-not-found", expectedScope, claims.Subject)
			writeError(w, http.StatusNotFound, "secret not found")
			return
		}
		s.audit.Emit(audit.Event{
			Category: "hecate", Outcome: "vault-read-failed",
			Message: err.Error(),
			Fields:  map[string]string{"ref": ref, "sub": claims.Subject},
		})
		writeError(w, http.StatusBadGateway, "vault read failed")
		return
	}

	s.audit.Emit(audit.Event{
		Category: "hecate", Outcome: "fetch",
		Fields: map[string]string{
			"ref": ref,
			"sub": claims.Subject,
			"jti": claims.JTI,
		},
	})

	// Return the credential as a plain JSON object. Callers expect
	// `{"value": "..."}` mirroring Vault's storage shape so the
	// transport layer between Hecate and the eventual MCP-protocol
	// fronting (Phase 2 K) is forward-compatible.
	writeJSON(w, http.StatusOK, map[string]string{"value": string(val)})
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
		Category: "hecate",
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
