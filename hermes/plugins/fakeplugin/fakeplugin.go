// Package fakeplugin is an in-memory hermes/core.Plugin for tests.
package fakeplugin

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/GoodOlClint/daedalus/hermes/core"
)

// Thread records a created thread and the posts made to it.
type Thread struct {
	Ref    string
	Parent string
	Title  string
	Opener string
	Posts  []core.Message
}

// Plugin is the fake implementation.
type Plugin struct {
	SurfaceName string

	mu      sync.Mutex
	threads map[string]*Thread
	nextID  int
	deliver core.InboundHandler

	// CreateThreadError is returned by CreateThread if non-nil — exercise
	// failure paths in tests.
	CreateThreadError error
	// PostError is returned by PostToThread if non-nil.
	PostError error
}

// New returns a Plugin that registers under the given surface name
// (typically "discord" or a test value).
func New(surface string) *Plugin {
	return &Plugin{SurfaceName: surface, threads: map[string]*Thread{}}
}

// Name satisfies core.Plugin.
func (p *Plugin) Name() string { return p.SurfaceName }

// Start satisfies core.Plugin.
func (p *Plugin) Start(_ context.Context, deliver core.InboundHandler) error {
	p.mu.Lock()
	p.deliver = deliver
	p.mu.Unlock()
	return nil
}

// Stop satisfies core.Plugin.
func (p *Plugin) Stop(_ context.Context) error { return nil }

// CreateThread satisfies core.Plugin.
func (p *Plugin) CreateThread(_ context.Context, req core.CreateThreadRequest) (string, error) {
	if p.CreateThreadError != nil {
		return "", p.CreateThreadError
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextID++
	ref := fmt.Sprintf("%s-thread-%d", p.SurfaceName, p.nextID)
	p.threads[ref] = &Thread{
		Ref:    ref,
		Parent: req.Parent,
		Title:  req.Title,
		Opener: req.Opener,
	}
	return ref, nil
}

// PostToThread satisfies core.Plugin.
func (p *Plugin) PostToThread(_ context.Context, threadRef string, msg core.Message) error {
	if p.PostError != nil {
		return p.PostError
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.threads[threadRef]
	if !ok {
		return fmt.Errorf("fake plugin: unknown thread %q", threadRef)
	}
	t.Posts = append(t.Posts, msg)
	return nil
}

// Deliver simulates an inbound message arriving from the surface. Returns
// an error if Start has not been called.
func (p *Plugin) Deliver(ctx context.Context, msg core.InboundMessage) error {
	p.mu.Lock()
	h := p.deliver
	p.mu.Unlock()
	if h == nil {
		return errors.New("fake plugin: deliver called before Start")
	}
	h(ctx, msg)
	return nil
}

// Threads returns a snapshot of every thread the plugin knows.
func (p *Plugin) Threads() []Thread {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Thread, 0, len(p.threads))
	for _, t := range p.threads {
		out = append(out, *t)
	}
	return out
}
