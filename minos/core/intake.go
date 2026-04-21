package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	hermescore "github.com/GoodOlClint/daedalus/hermes/core"
	"github.com/GoodOlClint/daedalus/pkg/audit"
	"github.com/GoodOlClint/daedalus/pkg/envelope"
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
// admin identity and passes /commission messages to the Commission path.
func (s *Server) handleInbound(ctx context.Context, msg hermescore.InboundMessage) {
	if s.cfg.Admin.Surface == "" || s.cfg.Admin.SurfaceID == "" {
		return
	}
	if msg.Surface != s.cfg.Admin.Surface || msg.SurfaceUserID != s.cfg.Admin.SurfaceID {
		// Non-admin — ignore (Phase 1 single-admin posture).
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
