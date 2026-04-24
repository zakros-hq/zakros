package iris

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Poller is the long-poll loop that drives Iris. One Poller per pod;
// goroutine-safe only in the sense that Run() is the only caller —
// not designed for concurrent invocation.
type Poller struct {
	Hermes  *HermesClient
	Handler *Handler

	// LongPollSeconds is the timeout passed to /hermes/events.next.
	// Default 25; the broker caps at 60. Setting it shorter increases
	// chatter; longer leaves the connection idle longer between
	// messages.
	LongPollSeconds int

	// MaxBatch is the per-poll event ceiling. Default 10 — Iris
	// processes events serially, so higher values just buffer more
	// in-memory.
	MaxBatch int

	Logger *slog.Logger
}

// Run loops until ctx is cancelled. Network failures back off briefly
// then retry — Phase 1 posture trades fancy backoff for code clarity.
func (p *Poller) Run(ctx context.Context) error {
	logger := p.Logger
	if logger == nil {
		logger = slog.Default()
	}
	since := uint64(0)
	timeout := p.LongPollSeconds
	if timeout <= 0 {
		timeout = 25
	}
	maxBatch := p.MaxBatch
	if maxBatch <= 0 {
		maxBatch = 10
	}

	logger.Info("iris poller starting", "timeout", timeout, "max_batch", maxBatch)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		events, err := p.Hermes.EventsNext(ctx, since, maxBatch, timeout)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return ctx.Err()
			}
			logger.Warn("events.next failed", "error", err)
			// Brief backoff so a misconfigured deploy doesn't loop hot.
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}

		for _, ev := range events {
			if err := p.Handler.Handle(ctx, ev); err != nil {
				logger.Error("handle message failed",
					"seq", ev.Seq,
					"surface", ev.Message.Surface,
					"thread", ev.Message.ThreadRef,
					"error", err)
				// Continue advancing `since` so a single bad message
				// doesn't block the loop. Idempotency lives in the
				// conversation store (MsgSeq dedup).
			}
			if ev.Seq > since {
				since = ev.Seq
			}
		}
	}
}

// String reports the poller's current cursor for diagnostics.
func (p *Poller) String() string {
	return fmt.Sprintf("iris.Poller{long_poll=%ds, max_batch=%d}", p.LongPollSeconds, p.MaxBatch)
}
