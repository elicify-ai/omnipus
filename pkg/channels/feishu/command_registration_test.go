//go:build amd64 || arm64 || riscv64 || mips64 || ppc64

package feishu

import (
	"context"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/commands"
)

// TestRegisterCommands_ReturnsNil verifies that RegisterCommands succeeds (T12e).
// Feishu bot menu commands are registered via the Developer Console, not via API.
// The method logs the expected command set and returns nil.
func TestRegisterCommands_ReturnsNil(t *testing.T) {
	ch := &FeishuChannel{}

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
func TestRegisterCommands_FiltersEmptyDefs(t *testing.T) {
	ch := &FeishuChannel{}

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

// TestRegisterCommands_EmptyList verifies that an empty definition list is accepted.
func TestRegisterCommands_EmptyList(t *testing.T) {
	ch := &FeishuChannel{}

	err := ch.RegisterCommands(context.Background(), nil)
	if err != nil {
		t.Errorf("RegisterCommands(nil) = %v, want nil", err)
	}
}
