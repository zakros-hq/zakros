package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	hermescore "github.com/zakros-hq/zakros/hermes/core"
	"github.com/zakros-hq/zakros/pkg/audit"
)

// IrisPullConsumer is the canonical name for Iris's pull buffer in the
// Hermes broker. Exposed so cmd/minos can pass it when wiring Hermes.
const IrisPullConsumer = "iris"

// irisPullCapacity bounds the in-memory buffer for the Iris consumer.
// Phase 1 posture: drop oldest on overflow. The buffer's purpose is
// near-real-time conversation pickup, not durable history — replay on
// Minos recovery lands in Phase 2 Slice I.
const irisPullCapacity = 256

// IrisPullFilter selects messages addressed to Iris: any inbound whose
// content (after trim) starts with "@iris" (case-insensitive).
//
// Slice 0 deliberately keeps this simple: Iris does not yet have its own
// surface identity (Slice I lands per-message identity webhooks), so
// a literal "@iris" prefix is what the operator actually types. Future
// surfaces grow native mention forms (Discord <@user_id> mentions, Slack
// app_mention events); the filter expands as those plugins wire in.
func IrisPullFilter(msg hermescore.InboundMessage) bool {
	t := strings.TrimSpace(msg.Content)
	if t == "" {
		return false
	}
	low := strings.ToLower(t)
	if strings.HasPrefix(low, "@iris") {
		return true
	}
	if strings.HasPrefix(low, "/iris") {
		return true
	}
	return false
}

// handleHermesEventsNext is the long-poll endpoint Iris hits to pull
// addressed messages. Query params:
//   - since:   last seq the consumer saw (default 0)
//   - max:     max events to return (default 32, capped at 100)
//   - timeout: long-poll timeout in seconds (default 25, capped at 60)
//
// Response is a JSON array of {seq, message} objects, possibly empty
// after a timeout. Empty + 200 is the "nothing new" signal.
func (s *Server) handleHermesEventsNext(w http.ResponseWriter, r *http.Request) {
	if s.hermes == nil {
		writeError(w, http.StatusServiceUnavailable, "hermes not configured")
		return
	}
	q := r.URL.Query()

	since := uint64(0)
	if raw := q.Get("since"); raw != "" {
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since")
			return
		}
		since = n
	}

	max := 32
	if raw := q.Get("max"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid max")
			return
		}
		if n > 100 {
			n = 100
		}
		max = n
	}

	timeout := 25 * time.Second
	if raw := q.Get("timeout"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid timeout")
			return
		}
		if n > 60 {
			n = 60
		}
		timeout = time.Duration(n) * time.Second
	}

	events, err := s.hermes.PullEvents(r.Context(), IrisPullConsumer, since, max, timeout)
	if err != nil {
		if errors.Is(err, hermescore.ErrUnknownConsumer) {
			writeError(w, http.StatusServiceUnavailable, "iris consumer not registered")
			return
		}
		// Context cancellation on client disconnect — return 200 + empty
		// so the client sees a normal "no events" response.
		writeJSON(w, http.StatusOK, []hermescore.PullEvent{})
		return
	}
	if events == nil {
		events = []hermescore.PullEvent{}
	}
	writeJSON(w, http.StatusOK, events)
}

// postAsIrisRequest is the body Iris POSTs to /hermes/post_as_iris.
type postAsIrisRequest struct {
	Surface   string `json:"surface"`
	ThreadRef string `json:"thread_ref"`
	Content   string `json:"content"`
}

// handleHermesPostAsIris posts an outbound message to a thread on Iris's
// behalf. Phase 1 posture: posts via the existing Hermes Plugin (bot
// identity, no per-message identity override). Phase 2 Slice I adds the
// webhook-based per-message Identity{Name:"Iris"} rendering so the post
// appears as a distinct speaker on each surface.
func (s *Server) handleHermesPostAsIris(w http.ResponseWriter, r *http.Request) {
	if s.hermes == nil {
		writeError(w, http.StatusServiceUnavailable, "hermes not configured")
		return
	}
	var body postAsIrisRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode body: %v", err))
		return
	}
	if body.Surface == "" || body.ThreadRef == "" || body.Content == "" {
		writeError(w, http.StatusBadRequest, "surface, thread_ref, content required")
		return
	}
	msg := hermescore.Message{
		Kind:    hermescore.KindStatus,
		Content: body.Content,
	}
	if err := s.hermes.PostToThread(r.Context(), body.Surface, body.ThreadRef, msg); err != nil {
		s.audit.Emit(audit.Event{
			Category: "iris",
			Outcome:  "post-failed",
			Message:  err.Error(),
			Fields:   map[string]string{"surface": body.Surface, "thread": body.ThreadRef},
		})
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit.Emit(audit.Event{
		Category: "iris",
		Outcome:  "posted",
		Fields:   map[string]string{"surface": body.Surface, "thread": body.ThreadRef},
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
