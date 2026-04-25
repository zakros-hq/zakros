package discord_test

import (
	"testing"

	"github.com/zakros-hq/zakros/hermes/plugins/discord"
)

func TestNewRequiresToken(t *testing.T) {
	if _, err := discord.New(discord.Config{WatchChannelID: "x"}); err == nil {
		t.Error("expected error when token missing")
	}
}

func TestNewRequiresWatchChannel(t *testing.T) {
	if _, err := discord.New(discord.Config{Token: "x"}); err == nil {
		t.Error("expected error when watch_channel_id missing")
	}
}

func TestNewNameMatches(t *testing.T) {
	p, err := discord.New(discord.Config{Token: "fake-token", WatchChannelID: "123"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if p.Name() != "discord" {
		t.Errorf("name: %s", p.Name())
	}
}
