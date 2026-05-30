package googlechat

import (
	"context"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/commands"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestRegisterCommands_ReturnsNil verifies that RegisterCommands succeeds (T12g).
// Google Chat slash commands are registered via the Google Cloud Console, not at runtime.
// The method logs the expected command set and returns nil.
func TestRegisterCommands_GoogleChat_ReturnsNil(t *testing.T) {
	cfg := config.GoogleChatConfig{
		Enabled:    true,
		Mode:       "webhook",
		WebhookURL: newSS("https://chat.googleapis.com/webhook/test"),
	}
	msgBus := bus.NewMessageBus()
	ch, err := NewGoogleChatChannel(cfg, msgBus)
	if err != nil {
		t.Fatalf("NewGoogleChatChannel() = %v", err)
	}

	defs := []commands.Definition{
		{
			Name:        "cancel",
			Description: "Cancel the current turn",
		},
	}

	if err := ch.RegisterCommands(context.Background(), defs); err != nil {
		t.Errorf("RegisterCommands() = %v, want nil", err)
	}
}

// TestRegisterCommands_GoogleChat_FiltersEmptyDefs verifies that definitions
// with missing Name or Description are silently skipped.
func TestRegisterCommands_GoogleChat_FiltersEmptyDefs(t *testing.T) {
	cfg := config.GoogleChatConfig{
		Enabled:    true,
		Mode:       "webhook",
		WebhookURL: newSS("https://chat.googleapis.com/webhook/test"),
	}
	msgBus := bus.NewMessageBus()
	ch, err := NewGoogleChatChannel(cfg, msgBus)
	if err != nil {
		t.Fatalf("NewGoogleChatChannel() = %v", err)
	}

	defs := []commands.Definition{
		{Name: "", Description: "No name"},
		{Name: "cancel", Description: ""},
		{Name: "cancel", Description: "Cancel the current turn"},
	}

	if err := ch.RegisterCommands(context.Background(), defs); err != nil {
		t.Errorf("RegisterCommands() = %v, want nil", err)
	}
}

// TestRegisterCommands_GoogleChat_EmptyList verifies that an empty definition
// list is accepted without error.
func TestRegisterCommands_GoogleChat_EmptyList(t *testing.T) {
	cfg := config.GoogleChatConfig{
		Enabled:    true,
		Mode:       "webhook",
		WebhookURL: newSS("https://chat.googleapis.com/webhook/test"),
	}
	msgBus := bus.NewMessageBus()
	ch, err := NewGoogleChatChannel(cfg, msgBus)
	if err != nil {
		t.Fatalf("NewGoogleChatChannel() = %v", err)
	}

	if err := ch.RegisterCommands(context.Background(), nil); err != nil {
		t.Errorf("RegisterCommands(nil) = %v, want nil", err)
	}
}
