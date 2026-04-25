// Package discord is the Hermes surface plugin for Discord bots. It is
// the Phase 1 reference surface per environment.md §3 Phase 1 Hermes
// Surface. Authenticates with a single bot token, creates per-task
// threads under a configured parent channel, and delivers inbound
// messages to the broker.
package discord

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/zakros-hq/zakros/hermes/core"
)

// surfaceName is the name the plugin registers under with the Broker.
const surfaceName = "discord"

// Config is the plugin's per-deployment configuration.
type Config struct {
	// Token is the Discord bot token (full value, e.g. "Bot XYZ..." is
	// NOT required — discordgo prepends "Bot " automatically).
	Token string
	// WatchChannelID is the parent channel Minos watches for admin
	// commissions. Messages in this channel and in any thread descended
	// from it are forwarded to the broker.
	WatchChannelID string
}

// Plugin implements core.Plugin against Discord.
type Plugin struct {
	cfg     Config
	session *discordgo.Session
	deliver core.InboundHandler

	mu     sync.Mutex
	closed bool
}

// New constructs a Plugin. The Discord session is created eagerly so
// authentication failures surface at construction; the gateway is only
// opened by Start.
func New(cfg Config) (*Plugin, error) {
	if cfg.Token == "" {
		return nil, errors.New("discord: token required")
	}
	if cfg.WatchChannelID == "" {
		return nil, errors.New("discord: watch_channel_id required")
	}
	s, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("discord: new session: %w", err)
	}
	s.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentMessageContent
	return &Plugin{cfg: cfg, session: s}, nil
}

// Name implements core.Plugin.
func (p *Plugin) Name() string { return surfaceName }

// Start opens the gateway connection and registers the message handler.
func (p *Plugin) Start(ctx context.Context, deliver core.InboundHandler) error {
	p.mu.Lock()
	p.deliver = deliver
	p.mu.Unlock()

	p.session.AddHandler(p.onMessageCreate)
	// Open() blocks until the ready event or an error.
	if err := p.session.Open(); err != nil {
		return fmt.Errorf("discord: open gateway: %w", err)
	}
	return nil
}

// Stop closes the gateway connection.
func (p *Plugin) Stop(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	if err := p.session.Close(); err != nil {
		return fmt.Errorf("discord: close gateway: %w", err)
	}
	return nil
}

// CreateThread creates a public thread under the configured watch channel
// (or under req.Parent if set) and seeds it with the opener message.
func (p *Plugin) CreateThread(_ context.Context, req core.CreateThreadRequest) (string, error) {
	parent := req.Parent
	if parent == "" {
		parent = p.cfg.WatchChannelID
	}
	// Discord creates a thread off a starter message. Post the opener as
	// the starter, then create the thread bound to it.
	starter, err := p.session.ChannelMessageSend(parent, req.Opener)
	if err != nil {
		return "", fmt.Errorf("discord: starter message: %w", err)
	}
	th, err := p.session.MessageThreadStartComplex(parent, starter.ID, &discordgo.ThreadStart{
		Name:                req.Title,
		AutoArchiveDuration: 1440, // 24 hours
		Invitable:           true,
	})
	if err != nil {
		return "", fmt.Errorf("discord: create thread: %w", err)
	}
	return th.ID, nil
}

// PostToThread sends a message to an existing thread ID.
func (p *Plugin) PostToThread(_ context.Context, threadRef string, msg core.Message) error {
	content := formatMessage(msg)
	if _, err := p.session.ChannelMessageSend(threadRef, content); err != nil {
		return fmt.Errorf("discord: post: %w", err)
	}
	return nil
}

// onMessageCreate is called by discordgo for every message. We filter to
// the watched channel and its threads, then translate to the broker.
func (p *Plugin) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m == nil || m.Author == nil {
		return
	}
	// Avoid our own messages (echo chamber).
	if s.State != nil && s.State.User != nil && m.Author.ID == s.State.User.ID {
		return
	}
	if !p.watchesChannel(m.ChannelID) {
		return
	}

	p.mu.Lock()
	h := p.deliver
	p.mu.Unlock()
	if h == nil {
		return
	}
	timestamp := time.Time(m.Timestamp)
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	h(context.Background(), core.InboundMessage{
		Surface:       surfaceName,
		SurfaceUserID: m.Author.ID,
		ThreadRef:     m.ChannelID,
		Content:       m.Content,
		Timestamp:     timestamp,
	})
}

// watchesChannel reports whether a channel ID is the configured watch
// channel itself, or a thread whose parent is that channel.
func (p *Plugin) watchesChannel(channelID string) bool {
	if channelID == p.cfg.WatchChannelID {
		return true
	}
	ch, err := p.session.State.Channel(channelID)
	if err != nil {
		// Cold cache — fetch from API.
		ch, err = p.session.Channel(channelID)
		if err != nil {
			return false
		}
	}
	return ch.ParentID == p.cfg.WatchChannelID
}

// formatMessage renders a core.Message into a Discord-friendly string.
// Code blocks get triple-fenced with language hint; other kinds ship the
// content as-is.
func formatMessage(msg core.Message) string {
	switch msg.Kind {
	case core.KindCode:
		lang := msg.Language
		return "```" + lang + "\n" + msg.Content + "\n```"
	case core.KindThinking:
		return "_thinking:_ " + msg.Content
	case core.KindHuman:
		return "❓ " + msg.Content
	case core.KindSummary:
		return "**summary:** " + msg.Content
	default:
		return msg.Content
	}
}

// compile-time check that Plugin satisfies core.Plugin.
var _ core.Plugin = (*Plugin)(nil)
