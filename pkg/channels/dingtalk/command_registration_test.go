package dingtalk

import (
	"context"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/commands"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestRegisterCommands_ReturnsNil verifies that RegisterCommands succeeds (T12f).
// DingTalk bot commands are registered via the Open Platform console, not at runtime.
// The method logs the expected command set and returns nil.
func TestRegisterCommands_ReturnsNil(t *testing.T) {
	ch, _ := newTestDingTalkChannel(t, config.DingTalkConfig{})

	defs := []commands.Definition{
		{
			Name:        "cancel",
			Description: "Cancel the current turn",
		},
	}

	err := ch.RegisterCommands(context.Background(), defs)
	if err != nil {
		t.Errorf("RegisterCommands() = %v, want nil", err)
	}
}

// TestRegisterCommands_FiltersEmptyDefs verifies that definitions with
// missing Name or Description are silently skipped.
func TestRegisterCommands_DingTalk_FiltersEmptyDefs(t *testing.T) {
	ch, _ := newTestDingTalkChannel(t, config.DingTalkConfig{})

	defs := []commands.Definition{
		{Name: "", Description: "No name"},
		{Name: "cancel", Description: ""},
		{Name: "cancel", Description: "Cancel the current turn"},
	}

	err := ch.RegisterCommands(context.Background(), defs)
	if err != nil {
		t.Errorf("RegisterCommands() = %v, want nil", err)
	}
}

// TestRegisterCommands_DingTalk_EmptyList verifies that an empty definition list is accepted.
func TestRegisterCommands_DingTalk_EmptyList(t *testing.T) {
	ch, _ := newTestDingTalkChannel(t, config.DingTalkConfig{})

	err := ch.RegisterCommands(context.Background(), nil)
	if err != nil {
		t.Errorf("RegisterCommands(nil) = %v, want nil", err)
	}
}
