package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/zakros-hq/zakros/pkg/audit"
	"github.com/zakros-hq/zakros/pkg/brokerauth"
	"github.com/zakros-hq/zakros/pkg/githubapp"
)

// server bundles the broker's runtime dependencies. One per process.
type server struct {
	logger         *slog.Logger
	audit          audit.Emitter
	verifier       *brokerauth.Verifier
	github         *githubapp.Client
	installationID int64
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	// /github/installation-token — JWT-gated, scope=clone. Returns a
	// per-call installation access token scoped to the requested
	// repo. The pod uses it as GITHUB_TOKEN for git clone + push +
	// gh CLI calls.
	mux.Handle("POST /github/installation-token",
		s.verifier.Require("clone", http.HandlerFunc(s.handleInstallationToken)))
	return mux
}

func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// installationTokenRequest is the body shape pods POST.
type installationTokenRequest struct {
	// Repo is the owner/name pair the pod wants a token for. Phase 1
	// the App is installed on a single repo, so the broker mints
	// against this exact value; if the App isn't installed on it,
	// GitHub returns 404 and the broker propagates a 502.
	Repo string `json:"repo"`
}

// installationTokenResponse mirrors githubapp.InstallationToken.
type installationTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

func (s *server) handleInstallationToken(w http.ResponseWriter, r *http.Request) {
	claims := brokerauth.ClaimsFromContext(r.Context())
	var body installationTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode body: %v", err))
		return
	}
	if body.Repo == "" {
		writeError(w, http.StatusBadRequest, "repo required (owner/name)")
		return
	}
	// GitHub's POST /app/installations/{id}/access_tokens `repositories`
	// parameter takes plain repo names, not owner/name pairs (slashes
	// aren't valid repo names). The pod sends owner/name because
	// that's what it has from envelope.Execution.RepoURL; we strip
	// the owner here. Owner is captured in audit for cross-reference.
	owner, repoName, ok := strings.Cut(body.Repo, "/")
	if !ok || owner == "" || repoName == "" {
		writeError(w, http.StatusBadRequest, "repo must be owner/name")
		return
	}

	tok, err := s.github.MintInstallationToken(r.Context(), s.installationID, []string{repoName})
	if err != nil {
		s.audit.Emit(audit.Event{
			Category: "github-broker",
			Outcome:  "mint-failed",
			Message:  err.Error(),
			Fields: map[string]string{
				"sub":  claims.Subject,
				"jti":  claims.JTI,
				"repo": body.Repo,
			},
		})
		writeError(w, http.StatusBadGateway, fmt.Sprintf("mint installation token: %v", err))
		return
	}

	s.audit.Emit(audit.Event{
		Category: "github-broker",
		Outcome:  "minted",
		Fields: map[string]string{
			"sub":        claims.Subject,
			"jti":        claims.JTI,
			"repo":       body.Repo,
			"expires_at": tok.ExpiresAt.Format("2006-01-02T15:04:05Z"),
		},
	})
	writeJSON(w, http.StatusOK, installationTokenResponse{
		Token:     tok.Token,
		ExpiresAt: tok.ExpiresAt.Format("2006-01-02T15:04:05Z"),
	})
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
