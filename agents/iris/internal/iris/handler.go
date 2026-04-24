package iris

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// systemPrompt frames Iris's role and constrains its tool use. Kept
// short — the tool definitions carry the bulk of the contract.
const systemPrompt = `You are Iris, the conversational interface to Project Daedalus.

You answer two kinds of operator requests on Discord and (in Phase 2) Slack:
1. State queries — "what's running?", "what just finished?", "show the queue". Use the query_state tool.
2. Commissions — "start a task to fix bug X", "commission a feature for Y". Use the commission tool.

Keep replies short and concrete. When you commission a task, confirm what you commissioned and the resulting task id. When you answer a state query, list tasks with their state and short summaries — do not invent data not returned by the tool.

You are talking to the operator; the message you receive is a single utterance, sometimes prefixed with "@iris" or "/iris" — strip that before reasoning. If the request is ambiguous, ask one clarifying question rather than guessing.`

// Handler processes one inbound Hermes message: load conversation
// state, run the Anthropic tool-use loop, post the final reply, and
// persist the new turn(s).
type Handler struct {
	Hermes        *HermesClient
	Anthropic     *AnthropicClient
	Tools         *ToolSet
	Conversations *ConversationStore

	// MaxToolRounds bounds the tool-use loop so a runaway model doesn't
	// hammer Minos forever. Phase 1: 6 rounds is plenty for a "list +
	// commission" pattern.
	MaxToolRounds int
}

// Handle processes one PullEvent. Errors are returned to the caller for
// logging; partial progress (a posted reply but failed persistence) is
// idempotent because Iris re-reads the message and the conversation
// store dedupes by MsgSeq.
func (h *Handler) Handle(ctx context.Context, ev PullEvent) error {
	msg := ev.Message
	if msg.Surface == "" || msg.ThreadRef == "" || msg.SurfaceUserID == "" {
		return fmt.Errorf("iris handler: incomplete message (seq=%d)", ev.Seq)
	}

	already, err := h.Conversations.AlreadyHandled(ctx, msg.Surface, msg.ThreadRef, msg.SurfaceUserID, ev.Seq)
	if err != nil {
		return fmt.Errorf("iris handler: dedupe check: %w", err)
	}
	if already {
		return nil
	}

	user := stripIrisPrefix(msg.Content)
	if user == "" {
		// Just "@iris" with nothing else — be friendly and prompt.
		if err := h.Hermes.PostAsIris(ctx, msg.Surface, msg.ThreadRef,
			"Hi! Ask me what's running, or tell me what to commission."); err != nil {
			return err
		}
		_, err := h.Conversations.AppendTurn(ctx, msg.Surface, msg.ThreadRef, msg.SurfaceUserID, Turn{
			Role:      "user",
			Content:   msg.Content,
			Timestamp: msg.Timestamp,
			MsgSeq:    ev.Seq,
		})
		return err
	}

	conv, err := h.Conversations.AppendTurn(ctx, msg.Surface, msg.ThreadRef, msg.SurfaceUserID, Turn{
		Role:      "user",
		Content:   user,
		Timestamp: msg.Timestamp,
		MsgSeq:    ev.Seq,
	})
	if err != nil {
		return fmt.Errorf("iris handler: append user turn: %w", err)
	}

	reply, err := h.runLoop(ctx, conv, commissionContext{
		Surface:       msg.Surface,
		SurfaceUserID: msg.SurfaceUserID,
		ThreadRef:     msg.ThreadRef,
	})
	if err != nil {
		// Surface the error to the operator instead of failing silently —
		// helps debugging on the real deployment.
		_ = h.Hermes.PostAsIris(ctx, msg.Surface, msg.ThreadRef,
			fmt.Sprintf("iris error: %v", err))
		return err
	}

	if err := h.Hermes.PostAsIris(ctx, msg.Surface, msg.ThreadRef, reply); err != nil {
		return fmt.Errorf("iris handler: post reply: %w", err)
	}

	if _, err := h.Conversations.AppendTurn(ctx, msg.Surface, msg.ThreadRef, msg.SurfaceUserID, Turn{
		Role:      "assistant",
		Content:   reply,
		Timestamp: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("iris handler: append assistant turn: %w", err)
	}
	return nil
}

// runLoop drives the tool-use exchange with Anthropic until a final
// text reply is produced or MaxToolRounds is exceeded.
func (h *Handler) runLoop(ctx context.Context, conv *Conversation, cc commissionContext) (string, error) {
	messages := buildMessageHistory(conv)
	rounds := h.MaxToolRounds
	if rounds <= 0 {
		rounds = 6
	}
	for i := 0; i < rounds; i++ {
		resp, err := h.Anthropic.Create(ctx, CreateRequest{
			System:    systemPrompt,
			Messages:  messages,
			Tools:     h.Tools.Definitions(),
			MaxTokens: 1024,
		})
		if err != nil {
			return "", fmt.Errorf("anthropic call: %w", err)
		}

		toolUses, finalText := splitContent(resp.Content)
		if resp.StopReason != "tool_use" || len(toolUses) == 0 {
			// Final text — return it. If the model produced no text but
			// also no tool calls, surface a fallback so the operator
			// isn't stared at by silence.
			if strings.TrimSpace(finalText) == "" {
				return "(no reply)", nil
			}
			return strings.TrimSpace(finalText), nil
		}

		// Append the assistant's tool-use turn verbatim, then build the
		// user turn carrying tool_results back.
		messages = append(messages, Message{
			Role:    "assistant",
			Content: contentBlocksAny(resp.Content),
		})

		results := make([]ContentBlock, 0, len(toolUses))
		for _, tu := range toolUses {
			out, err := h.Tools.Run(ctx, tu.Name, tu.Input, cc)
			if err != nil {
				if errors.Is(err, ErrToolError) {
					results = append(results, ContentBlock{
						Type:      "tool_result",
						ToolUseID: tu.ID,
						Content:   err.Error(),
						IsError:   true,
					})
					continue
				}
				return "", err
			}
			results = append(results, ContentBlock{
				Type:      "tool_result",
				ToolUseID: tu.ID,
				Content:   out,
			})
		}
		messages = append(messages, Message{
			Role:    "user",
			Content: contentBlocksAny(results),
		})
	}
	return "", fmt.Errorf("iris: tool-use loop exceeded %d rounds", rounds)
}

// buildMessageHistory turns persisted Turns into Anthropic Messages.
// Plain string content (Phase 1 turns are all simple text) — assistant
// turns from prior rounds in this conversation are collapsed to text
// even if they originally involved tool use, since intermediate tool
// uses aren't persisted.
func buildMessageHistory(conv *Conversation) []Message {
	out := make([]Message, 0, len(conv.Turns))
	for _, t := range conv.Turns {
		out = append(out, Message{Role: t.Role, Content: t.Content})
	}
	return out
}

// splitContent walks the response content blocks and returns
// (tool_use blocks, concatenated text). Useful for deciding whether
// the loop continues.
func splitContent(blocks []ContentBlock) ([]ContentBlock, string) {
	var tools []ContentBlock
	var text strings.Builder
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			tools = append(tools, b)
		case "text":
			text.WriteString(b.Text)
		}
	}
	return tools, text.String()
}

// contentBlocksAny converts a typed slice into the Message.Content
// shape (which is `any` so a request body can be either string or
// []ContentBlock). Identity-ish; carrying the slice through `any`
// makes JSON encoding emit the array shape.
func contentBlocksAny(blocks []ContentBlock) []ContentBlock {
	return blocks
}

// stripIrisPrefix removes a leading "@iris" or "/iris" mention and the
// space that follows. Case-insensitive. Returns the cleaned content.
func stripIrisPrefix(s string) string {
	t := strings.TrimSpace(s)
	low := strings.ToLower(t)
	for _, p := range []string{"@iris", "/iris"} {
		if strings.HasPrefix(low, p) {
			t = strings.TrimSpace(t[len(p):])
			return t
		}
	}
	return t
}
