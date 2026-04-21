package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	hermescore "github.com/GoodOlClint/daedalus/hermes/core"
	"github.com/GoodOlClint/daedalus/minos/core"
	"github.com/GoodOlClint/daedalus/minos/storage"
)

func TestParseCommissionHappy(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want core.CommissionRequest
	}{
		{
			"basic",
			`/commission repo=https://github.com/x/y branch=fix/a fix the widget`,
			core.CommissionRequest{
				Execution: core.ExecutionRequest{RepoURL: "https://github.com/x/y", Branch: "fix/a"},
			},
		},
		{
			"quoted values and base branch",
			`/commission repo="https://github.com/x/y" branch="feature/space in name" base=develop summary with spaces`,
			core.CommissionRequest{
				Execution: core.ExecutionRequest{RepoURL: "https://github.com/x/y", Branch: "feature/space in name", BaseBranch: "develop"},
			},
		},
		{
			"reordered keys",
			`/commission branch=fix/a repo=https://example.com/r do it`,
			core.CommissionRequest{
				Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "fix/a"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := core.ParseCommissionCommand(tc.in)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got.Execution.RepoURL != tc.want.Execution.RepoURL {
				t.Errorf("repo: got %q want %q", got.Execution.RepoURL, tc.want.Execution.RepoURL)
			}
			if got.Execution.Branch != tc.want.Execution.Branch {
				t.Errorf("branch: got %q want %q", got.Execution.Branch, tc.want.Execution.Branch)
			}
			if got.Execution.BaseBranch != tc.want.Execution.BaseBranch {
				t.Errorf("base: got %q want %q", got.Execution.BaseBranch, tc.want.Execution.BaseBranch)
			}
			if got.Brief.Summary == "" {
				t.Error("summary empty")
			}
		})
	}
}

func TestParseCommissionErrors(t *testing.T) {
	cases := []struct{ name, in string }{
		{"not a command", "hello"},
		{"missing repo", `/commission branch=x summary`},
		{"missing branch", `/commission repo=x summary`},
		{"missing summary", `/commission repo=x branch=y`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := core.ParseCommissionCommand(tc.in)
			if err == nil {
				t.Errorf("expected error")
			}
			if tc.name == "not a command" && !errors.Is(err, core.ErrNotACommand) {
				t.Errorf("expected ErrNotACommand, got %v", err)
			}
		})
	}
}

func TestIntakeCommissionsTask(t *testing.T) {
	kit, plug := newTestServerWithHermes(t)

	err := plug.Deliver(context.Background(), hermescore.InboundMessage{
		Surface:       "discord",
		SurfaceUserID: "admin-id",
		ThreadRef:     "channel-ops",
		Timestamp:     time.Now().UTC(),
		Content:       `/commission repo=https://example.com/r branch=fix/a please fix the thing`,
	})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}

	tasks, _ := kit.store.ListTasks(context.Background(), nil, 0)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task commissioned via intake, got %d", len(tasks))
	}
	if tasks[0].State != storage.StateRunning {
		t.Errorf("expected running after intake, got %s", tasks[0].State)
	}
	if tasks[0].Envelope.Brief.Summary != "please fix the thing" {
		t.Errorf("summary lost: %q", tasks[0].Envelope.Brief.Summary)
	}
}

func TestIntakeIgnoresNonAdmin(t *testing.T) {
	kit, plug := newTestServerWithHermes(t)

	_ = plug.Deliver(context.Background(), hermescore.InboundMessage{
		Surface:       "discord",
		SurfaceUserID: "some-other-user",
		ThreadRef:     "channel-ops",
		Content:       `/commission repo=https://example.com/r branch=fix/a fix`,
	})

	tasks, _ := kit.store.ListTasks(context.Background(), nil, 0)
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks from non-admin, got %d", len(tasks))
	}
}

func TestIntakePostsErrorReply(t *testing.T) {
	kit, plug := newTestServerWithHermes(t)
	// First make sure the admin has a thread known to Hermes by letting
	// the admin post a malformed command — the reply lands in that thread.
	_, err := kit.server.Commission(context.Background(), core.CommissionRequest{})
	_ = err // expected failure, only to populate audit

	// Deliver a malformed commission from the admin to a thread the plugin
	// knows about (ThreadRef is just a string the fake accepts).
	// Set up a known thread first:
	threadRef, err := plug.CreateThread(context.Background(), hermescore.CreateThreadRequest{
		Parent: "channel-ops", Title: "t", Opener: "hi",
	})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}

	_ = plug.Deliver(context.Background(), hermescore.InboundMessage{
		Surface:       "discord",
		SurfaceUserID: "admin-id",
		ThreadRef:     threadRef,
		Content:       `/commission repo=onlything`, // missing branch + summary
	})

	threads := plug.Threads()
	var posted bool
	for _, th := range threads {
		if th.Ref != threadRef {
			continue
		}
		for _, p := range th.Posts {
			if p.Kind == hermescore.KindStatus {
				posted = true
			}
		}
	}
	if !posted {
		t.Errorf("expected an error reply posted to the admin's thread")
	}
}
