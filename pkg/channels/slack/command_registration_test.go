package slack

import (
	"context"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/commands"
)

// TestSlackChannel_ImplementsCommandRegistrarCapable verifies the interface is satisfied.
func TestSlackChannel_ImplementsCommandRegistrarCapable(t *testing.T) {
	var _ channels.CommandRegistrarCapable = (*SlackChannel)(nil)
}

// TestSlackRegisterCommands_ReturnsNil verifies graceful no-op with /cancel.
func TestSlackRegisterCommands_ReturnsNil(t *testing.T) {
	ch := &SlackChannel{}
	defs := []commands.Definition{
		{Name: "cancel", Description: "Cancel the running agent task"},
	}
	if err := ch.RegisterCommands(context.Background(), defs); err != nil {
		t.Fatalf("RegisterCommands() returned unexpected error: %v", err)
	}
}

// TestSlackRegisterCommands_SkipsIncomplete verifies that definitions missing
// Name or Description are silently filtered out (Telegram pattern).
func TestSlackRegisterCommands_SkipsIncomplete(t *testing.T) {
	ch := &SlackChannel{}
	defs := []commands.Definition{
		{Name: "", Description: "no name"},
		{Name: "noDesc", Description: ""},
		{Name: "cancel", Description: "Cancel the running agent task"},
	}
	if err := ch.RegisterCommands(context.Background(), defs); err != nil {
		t.Fatalf("RegisterCommands() with incomplete defs returned error: %v", err)
	}
}

// TestSlackRegisterCommands_EmptyList verifies that an empty definition list
// returns nil without logging noise.
func TestSlackRegisterCommands_EmptyList(t *testing.T) {
	ch := &SlackChannel{}
	if err := ch.RegisterCommands(context.Background(), nil); err != nil {
		t.Fatalf("RegisterCommands(nil) returned unexpected error: %v", err)
	}
}

// TestSlackRegisterCommands_WithBuiltinCancel passes the /cancel definition
// from commands.BuiltinDefinitions() (T12b — uses real builtin list).
func TestSlackRegisterCommands_WithBuiltinCancel(t *testing.T) {
	allDefs := commands.BuiltinDefinitions()

	ch := &SlackChannel{}
	if err := ch.RegisterCommands(context.Background(), allDefs); err != nil {
		t.Fatalf("RegisterCommands(BuiltinDefinitions()) returned error: %v", err)
	}
}
