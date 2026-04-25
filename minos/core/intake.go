package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	hermescore "github.com/zakros-hq/zakros/hermes/core"
	"github.com/zakros-hq/zakros/minos/storage"
	"github.com/zakros-hq/zakros/pkg/audit"
	"github.com/zakros-hq/zakros/pkg/envelope"
)

// ErrNotACommand is returned by ParseCommissionCommand for messages that
// don't carry the /commission prefix.
var ErrNotACommand = errors.New("not a commission command")

// intakeKeyValue matches "repo=<value>" / "branch=<value>" tokens. Values
// may be plain (no spaces) or quoted with double quotes.
var intakeKeyValue = regexp.MustCompile(`^(repo|branch|base)=(?:"([^"]*)"|(\S+))\s*`)

// ParseCommissionCommand parses a Discord/Slack-style `/commission` message
// into a CommissionRequest. Expected format:
//
//	/commission repo=<url> branch=<branch> [base=<base-branch>] <summary>
//
// The summary is whatever follows the last recognised key=value token.
// Unknown keywords before the summary are an error so typos don't silently
// become part of the brief.
func ParseCommissionCommand(text string) (CommissionRequest, error) {
	const prefix = "/commission"
	trimmed := strings.TrimSpace(text)
	rest := strings.TrimPrefix(trimmed, prefix)
	if rest == trimmed {
		return CommissionRequest{}, ErrNotACommand
	}
	rest = strings.TrimLeft(rest, " \t")

	var repo, branch, base string
	for {
		m := intakeKeyValue.FindStringSubmatchIndex(rest)
		if m == nil {
			break
		}
		key := rest[m[2]:m[3]]
		var val string
		if m[4] != -1 {
			val = rest[m[4]:m[5]]
		} else {
			val = rest[m[6]:m[7]]
		}
		switch key {
		case "repo":
			repo = val
		case "branch":
			branch = val
		case "base":
			base = val
		}
		rest = rest[m[1]:]
	}
	summary := strings.TrimSpace(rest)

	if repo == "" {
		return CommissionRequest{}, fmt.Errorf("repo= required")
	}
	if branch == "" {
		return CommissionRequest{}, fmt.Errorf("branch= required")
	}
	if summary == "" {
		return CommissionRequest{}, fmt.Errorf("summary required")
	}

	return CommissionRequest{
		TaskType: envelope.TaskTypeCode,
		Brief:    envelope.Brief{Summary: summary},
		Execution: ExecutionRequest{
			RepoURL:    repo,
			Branch:     branch,
			BaseBranch: base,
		},
	}, nil
}

// handleInbound is the hermes InboundHandler Minos subscribes at startup
// when Hermes is wired in. It accepts only messages from the configured
// admin identity and dispatches /commission and /status commands.
func (s *Server) handleInbound(ctx context.Context, msg hermescore.InboundMessage) {
	if s.cfg.Admin.Surface == "" || s.cfg.Admin.SurfaceID == "" {
		return
	}
	if msg.Surface != s.cfg.Admin.Surface || msg.SurfaceUserID != s.cfg.Admin.SurfaceID {
		// Non-admin — ignore (Phase 1 single-admin posture).
		return
	}
	trimmed := strings.TrimSpace(msg.Content)
	// /status: list recent tasks — the Phase 1 minimum for "what's
	// running?" per gate bullet 2. Iris-as-pod (with Ollama) refines
	// this into true conversational state queries.
	if trimmed == "/status" || strings.HasPrefix(trimmed, "/status ") ||
		strings.EqualFold(trimmed, "what's running?") ||
		strings.EqualFold(trimmed, "what is running?") {
		s.handleStatusQuery(ctx, msg)
		return
	}
	req, err := ParseCommissionCommand(msg.Content)
	if err != nil {
		if errors.Is(err, ErrNotACommand) {
			// Free-form chatter in admin thread is not a command; ignore.
			return
		}
		// Malformed /commission — reply via the thread so the admin sees
		// the parse error.
		s.audit.Emit(audit.Event{
			Category: "intake",
			Outcome:  "parse-failed",
			Message:  err.Error(),
			Fields:   map[string]string{"surface": msg.Surface, "user": msg.SurfaceUserID},
		})
		if s.hermes != nil && msg.ThreadRef != "" {
			_ = s.hermes.PostToThread(ctx, msg.Surface, msg.ThreadRef, hermescore.Message{
				Kind:    hermescore.KindStatus,
				Content: fmt.Sprintf("commission rejected: %s", err.Error()),
			})
		}
		return
	}
	req.Origin = envelope.Origin{
		Surface:   "hermes:" + msg.Surface,
		RequestID: fmt.Sprintf("%s:%s", msg.ThreadRef, msg.Timestamp.Format("20060102T150405Z")),
		Requester: msg.SurfaceUserID,
	}
	task, err := s.Commission(ctx, req)
	if err != nil {
		s.audit.Emit(audit.Event{
			Category: "intake",
			Outcome:  "commission-failed",
			Message:  err.Error(),
			Fields:   map[string]string{"surface": msg.Surface, "user": msg.SurfaceUserID},
		})
		if s.hermes != nil && msg.ThreadRef != "" {
			_ = s.hermes.PostToThread(ctx, msg.Surface, msg.ThreadRef, hermescore.Message{
				Kind:    hermescore.KindStatus,
				Content: fmt.Sprintf("commission failed: %s", err.Error()),
			})
		}
		return
	}
	s.audit.Emit(audit.Event{
		Category: "intake",
		Outcome:  "commissioned",
		Fields:   map[string]string{"task_id": task.ID.String(), "user": msg.SurfaceUserID},
	})
}

// handleStatusQuery replies to "what's running?" with a compact summary
// of active tasks: queued, running, and awaiting-review. Phase 1 minimum
// for gate bullet 2 — Iris-as-pod deepens this into a conversational
// surface in Slice E proper.
func (s *Server) handleStatusQuery(ctx context.Context, msg hermescore.InboundMessage) {
	active := []storage.State{storage.StateQueued, storage.StateRunning, storage.StateAwaitingReview}
	tasks, err := s.store.ListTasks(ctx, active, 20)
	if err != nil {
		s.reply(ctx, msg, fmt.Sprintf("status failed: %s", err.Error()))
		return
	}
	if len(tasks) == 0 {
		s.reply(ctx, msg, "no active tasks")
		return
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d active task(s):\n", len(tasks)))
	for _, t := range tasks {
		summary := ""
		if t.Envelope != nil {
			summary = t.Envelope.Brief.Summary
		}
		b.WriteString(fmt.Sprintf("- %s [%s] %s\n", t.ID.String()[:8], t.State, summary))
	}
	s.reply(ctx, msg, b.String())
}

// reply posts a thread message back to the originating surface when a
// broker + thread_ref are available.
func (s *Server) reply(ctx context.Context, msg hermescore.InboundMessage, content string) {
	if s.hermes == nil || msg.ThreadRef == "" {
		return
	}
	_ = s.hermes.PostToThread(ctx, msg.Surface, msg.ThreadRef, hermescore.Message{
		Kind:    hermescore.KindStatus,
		Content: content,
	})
}
