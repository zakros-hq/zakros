package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/GoodOlClint/daedalus/hermes/core"
	"github.com/GoodOlClint/daedalus/hermes/plugins/fakeplugin"
)

func TestBrokerCreateAndPost(t *testing.T) {
	b := core.New()
	p := fakeplugin.New("discord")
	if err := b.RegisterPlugin(p); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := b.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	ref, err := b.CreateThread(context.Background(), "discord", core.CreateThreadRequest{
		Parent: "channel-1",
		Title:  "task-xyz",
		Opener: "New task commissioned",
	})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	if err := b.PostToThread(context.Background(), "discord", ref, core.Message{
		Kind: core.KindStatus, Content: "working...",
	}); err != nil {
		t.Fatalf("post: %v", err)
	}
	threads := p.Threads()
	if len(threads) != 1 || len(threads[0].Posts) != 1 {
		t.Fatalf("unexpected plugin state: %+v", threads)
	}
	if threads[0].Posts[0].Content != "working..." {
		t.Errorf("post content: %q", threads[0].Posts[0].Content)
	}
}

func TestBrokerRoutingUnknownSurface(t *testing.T) {
	b := core.New()
	if _, err := b.CreateThread(context.Background(), "telegram", core.CreateThreadRequest{}); !errors.Is(err, core.ErrNoPlugin) {
		t.Errorf("expected ErrNoPlugin, got %v", err)
	}
	if err := b.PostToThread(context.Background(), "telegram", "ref", core.Message{}); !errors.Is(err, core.ErrNoPlugin) {
		t.Errorf("expected ErrNoPlugin, got %v", err)
	}
}

func TestBrokerDuplicateRegistration(t *testing.T) {
	b := core.New()
	_ = b.RegisterPlugin(fakeplugin.New("discord"))
	if err := b.RegisterPlugin(fakeplugin.New("discord")); err == nil {
		t.Fatal("expected duplicate-plugin error")
	}
}

func TestBrokerInboundFanOut(t *testing.T) {
	b := core.New()
	p := fakeplugin.New("discord")
	_ = b.RegisterPlugin(p)

	var wg sync.WaitGroup
	var h1Count, h2Count int
	var mu sync.Mutex

	b.Subscribe(func(_ context.Context, _ core.InboundMessage) {
		mu.Lock()
		h1Count++
		mu.Unlock()
		wg.Done()
	})
	b.Subscribe(func(_ context.Context, _ core.InboundMessage) {
		mu.Lock()
		h2Count++
		mu.Unlock()
		wg.Done()
	})

	if err := b.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	wg.Add(2)
	if err := p.Deliver(context.Background(), core.InboundMessage{
		Surface: "discord", SurfaceUserID: "u-1", ThreadRef: "t-1", Content: "hi",
	}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if h1Count != 1 || h2Count != 1 {
		t.Errorf("expected both handlers once each, got h1=%d h2=%d", h1Count, h2Count)
	}
}

func TestBrokerStopPropagatesErrors(t *testing.T) {
	b := core.New()
	p := fakeplugin.New("discord")
	_ = b.RegisterPlugin(p)
	_ = b.Start(context.Background())
	if err := b.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}
