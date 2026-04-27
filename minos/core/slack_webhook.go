package core

import (
	"encoding/json"
	"errors"
	"net/http"

	slackverify "github.com/zakros-hq/zakros/cerberus/verification/slack"
	"github.com/zakros-hq/zakros/pkg/audit"
)

// handleSlackWebhook authenticates a Slack webhook delivery, handles
// the url_verification handshake (echoing the challenge), and audits
// other event types as ingested. Slice J ships only the verifier +
// audit ingest; routing Slack events to running pods (so an Iris
// pod can react to app_mention) lands with Slice I when the Slack
// Hermes plugin extracts.
func (s *Server) handleSlackWebhook(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SlackWebhookSecretRef == "" {
		writeError(w, http.StatusServiceUnavailable, "slack webhook not configured")
		return
	}
	secret, err := s.provider.Resolve(r.Context(), s.cfg.SlackWebhookSecretRef)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "resolve slack signing secret")
		return
	}
	verifier := slackverify.NewVerifier(secret.Data, s.replayStore)
	event, err := verifier.Verify(r.Context(), r)
	if err != nil {
		switch {
		case errors.Is(err, slackverify.ErrInvalidSignature):
			writeError(w, http.StatusUnauthorized, "invalid signature")
		case errors.Is(err, slackverify.ErrMissingHeader):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, slackverify.ErrTimestampSkew):
			writeError(w, http.StatusBadRequest, "timestamp skew")
		case errors.Is(err, slackverify.ErrReplay):
			// Slack doesn't retransmit on 2xx, but defense in depth: 200 + noop.
			writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	// Slack's url_verification handshake: server must echo the
	// `challenge` field back as JSON. Done at install/registration
	// time only.
	if event.Type == "url_verification" {
		var probe struct {
			Challenge string `json:"challenge"`
		}
		if err := json.Unmarshal(event.Body, &probe); err != nil || probe.Challenge == "" {
			writeError(w, http.StatusBadRequest, "url_verification: missing challenge")
			return
		}
		s.audit.Emit(audit.Event{
			Category: "webhook", Outcome: "slack-url-verification-echoed",
			Fields: map[string]string{"delivery": event.DeliveryID},
		})
		writeJSON(w, http.StatusOK, map[string]string{"challenge": probe.Challenge})
		return
	}

	// Other event types (event_callback, block_actions, ...) are
	// audited and acknowledged. Routing into pod-side consumers
	// lands with Slice I.
	s.audit.Emit(audit.Event{
		Category: "webhook", Outcome: "slack-received",
		Fields: map[string]string{
			"type":     event.Type,
			"delivery": event.DeliveryID,
		},
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
