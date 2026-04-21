// Package core is the Hermes messaging broker. Phase 1 runs in-process
// with Minos per architecture.md §6 Communication Surfaces; Phase 2
// extracts it into its own service with subprocess-per-plugin isolation.
//
// The Go API exposed here (Broker) is what Minos calls directly. The HTTP
// surface pods reach via the thread sidecar wraps the same methods and
// lands in a follow-up commit.
package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrNoPlugin is returned when a caller references a surface for which no
// plugin is registered.
var ErrNoPlugin = errors.New("no plugin for surface")

// MessageKind enumerates the outbound message types the thread sidecar
// exposes per architecture.md §8 Pod Sidecars.
type MessageKind string

const (
	KindStatus   MessageKind = "status"
	KindThinking MessageKind = "thinking"
	KindCode     MessageKind = "code"
	KindSummary  MessageKind = "summary"
	KindHuman    MessageKind = "request_human_input"
)

// CreateThreadRequest describes a new task thread to create on a surface.
type CreateThreadRequest struct {
	// Parent identifies the surface-specific container the thread lives
	// under (a Discord channel ID, Slack channel ID, etc.).
	Parent string
	// Title is the human-visible thread title.
	Title string
	// Opener is the first message posted to the thread on creation.
	Opener string
}

// Message is one outbound message.
type Message struct {
	Kind     MessageKind
	Content  string
	Language string // set when Kind == KindCode
}

// InboundMessage is one message arriving from a surface's gateway.
type InboundMessage struct {
	Surface       string
	SurfaceUserID string
	ThreadRef     string
	Content       string
	Timestamp     time.Time
}

// InboundHandler receives messages delivered by any registered plugin.
type InboundHandler func(ctx context.Context, msg InboundMessage)

// Plugin is the contract every surface implementation satisfies.
type Plugin interface {
	// Name returns the surface identifier, e.g. "discord".
	Name() string

	// Start begins the plugin's gateway loop. deliver is called for every
	// inbound message. Start blocks until the plugin is ready to accept
	// outbound calls or returns an error.
	Start(ctx context.Context, deliver InboundHandler) error

	// Stop disconnects the plugin cleanly.
	Stop(ctx context.Context) error

	// CreateThread creates a new thread on the plugin's surface and
	// returns the surface-specific thread reference.
	CreateThread(ctx context.Context, req CreateThreadRequest) (string, error)

	// PostToThread posts a message to an existing thread.
	PostToThread(ctx context.Context, threadRef string, msg Message) error
}

// Broker is the Hermes core. It is safe for concurrent use.
type Broker struct {
	mu       sync.RWMutex
	plugins  map[string]Plugin
	handlers []InboundHandler
}

// New returns an empty Broker ready to accept plugin registrations.
func New() *Broker {
	return &Broker{plugins: map[string]Plugin{}}
}

// RegisterPlugin adds a plugin keyed by its Name. Calling twice with the
// same name returns an error.
func (b *Broker) RegisterPlugin(p Plugin) error {
	if p == nil {
		return errors.New("hermes: nil plugin")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	name := p.Name()
	if _, exists := b.plugins[name]; exists {
		return fmt.Errorf("hermes: plugin %q already registered", name)
	}
	b.plugins[name] = p
	return nil
}

// Subscribe adds an inbound handler. Every registered plugin will route
// its deliveries to every handler subscribed at the moment of delivery.
func (b *Broker) Subscribe(h InboundHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, h)
}

// Start launches every registered plugin. Inbound messages are fanned out
// to all subscribed handlers.
func (b *Broker) Start(ctx context.Context) error {
	b.mu.RLock()
	plugins := make([]Plugin, 0, len(b.plugins))
	for _, p := range b.plugins {
		plugins = append(plugins, p)
	}
	b.mu.RUnlock()

	for _, p := range plugins {
		if err := p.Start(ctx, b.deliver); err != nil {
			return fmt.Errorf("hermes: start plugin %q: %w", p.Name(), err)
		}
	}
	return nil
}

// Stop disconnects every plugin. The first error is returned but all
// plugins are attempted.
func (b *Broker) Stop(ctx context.Context) error {
	b.mu.RLock()
	plugins := make([]Plugin, 0, len(b.plugins))
	for _, p := range b.plugins {
		plugins = append(plugins, p)
	}
	b.mu.RUnlock()

	var firstErr error
	for _, p := range plugins {
		if err := p.Stop(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("hermes: stop plugin %q: %w", p.Name(), err)
		}
	}
	return firstErr
}

// CreateThread routes to the plugin matching surface.
func (b *Broker) CreateThread(ctx context.Context, surface string, req CreateThreadRequest) (string, error) {
	p, err := b.plugin(surface)
	if err != nil {
		return "", err
	}
	return p.CreateThread(ctx, req)
}

// PostToThread routes to the plugin matching surface.
func (b *Broker) PostToThread(ctx context.Context, surface, threadRef string, msg Message) error {
	p, err := b.plugin(surface)
	if err != nil {
		return err
	}
	return p.PostToThread(ctx, threadRef, msg)
}

func (b *Broker) plugin(surface string) (Plugin, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	p, ok := b.plugins[surface]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoPlugin, surface)
	}
	return p, nil
}

// deliver is the fan-out helper passed to every plugin's Start. It is a
// method on Broker so the closure captured by plugins keeps a stable
// reference through subsequent handler subscriptions.
func (b *Broker) deliver(ctx context.Context, msg InboundMessage) {
	b.mu.RLock()
	handlers := make([]InboundHandler, len(b.handlers))
	copy(handlers, b.handlers)
	b.mu.RUnlock()
	for _, h := range handlers {
		h(ctx, msg)
	}
}
