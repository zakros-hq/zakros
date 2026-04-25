package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	hermescore "github.com/zakros-hq/zakros/hermes/core"
	"github.com/zakros-hq/zakros/minos/core"
)

func TestIrisPullFilter(t *testing.T) {
	cases := []struct {
		content string
		want    bool
	}{
		{"@iris what's running", true},
		{"  @iris status?", true},
		{"@IRIS hello", true},
		{"/iris status", true},
		{"/commission repo=x branch=y do x", false},
		{"plain chat with no mention", false},
		{"", false},
		{"  ", false},
	}
	for _, tc := range cases {
		got := core.IrisPullFilter(hermescore.InboundMessage{Content: tc.content})
		if got != tc.want {
			t.Errorf("IrisPullFilter(%q) = %v, want %v", tc.content, got, tc.want)
		}
	}
}

func TestEventsNextDeliversAddressedMessages(t *testing.T) {
	kit, plug := newTestServerWithHermes(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	// Deliver one message addressed to Iris and one not. Only the addressed
	// one should land in the pull buffer.
	if err := plug.Deliver(context.Background(), hermescore.InboundMessage{
		Surface:       "discord",
		SurfaceUserID: "operator",
		ThreadRef:     "thread-1",
		Content:       "@iris what's running",
		Timestamp:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if err := plug.Deliver(context.Background(), hermescore.InboundMessage{
		Surface:       "discord",
		SurfaceUserID: "admin-id",
		ThreadRef:     "thread-1",
		Content:       "/commission repo=x branch=y something",
		Timestamp:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	req, _ := http.NewRequestWithContext(context.Background(), "GET",
		ts.URL+"/hermes/events.next?since=0&max=10&timeout=2", nil)
	req.Header.Set("Authorization", "Bearer iris-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("events.next: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var events []hermescore.PullEvent
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Message.Content != "@iris what's running" {
		t.Errorf("unexpected content: %q", events[0].Message.Content)
	}
	if events[0].Seq == 0 {
		t.Error("seq should be > 0")
	}
}

func TestEventsNextLongPollTimesOut(t *testing.T) {
	kit, _ := newTestServerWithHermes(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	start := time.Now()
	req, _ := http.NewRequestWithContext(context.Background(), "GET",
		ts.URL+"/hermes/events.next?since=0&timeout=1", nil)
	req.Header.Set("Authorization", "Bearer iris-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("events.next: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var events []hermescore.PullEvent
	_ = json.NewDecoder(resp.Body).Decode(&events)
	if len(events) != 0 {
		t.Errorf("expected 0 events on timeout, got %d", len(events))
	}
	if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
		t.Errorf("returned too fast: %v", elapsed)
	}
}

func TestPostAsIrisPosts(t *testing.T) {
	kit, plug := newTestServerWithHermes(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	// Seed a thread on the fake plugin so PostToThread has somewhere to land.
	threadRef, err := plug.CreateThread(context.Background(), hermescore.CreateThreadRequest{
		Parent: "channel-ops", Title: "t", Opener: "o",
	})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}

	body, _ := json.Marshal(map[string]string{
		"surface":    "discord",
		"thread_ref": threadRef,
		"content":    "running 2 tasks",
	})
	req, _ := http.NewRequestWithContext(context.Background(), "POST",
		ts.URL+"/hermes/post_as_iris", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer iris-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post_as_iris: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	threads := plug.Threads()
	var posts []hermescore.Message
	for _, th := range threads {
		if th.Ref == threadRef {
			posts = th.Posts
		}
	}
	if len(posts) != 1 || posts[0].Content != "running 2 tasks" {
		t.Errorf("expected 1 post with content, got %+v", posts)
	}
}

func TestEventsNextRequiresIrisAuth(t *testing.T) {
	kit, _ := newTestServerWithHermes(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/hermes/events.next")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}
