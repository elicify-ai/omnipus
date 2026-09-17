package tools

import (
	"context"
	"errors"
	"strings"
)

var ErrDelegatedAvaWrite = errors.New("Ava may write configuration only from an attended, user-owned session; switch to Ava in the user-owned session")

// ValidateConfigurationWriteContext is shared by mutation tools in this
// package and by sysagent tools without creating an import cycle.
func ValidateConfigurationWriteContext(ctx context.Context) error {
	if ToolAgentID(ctx) == "ava" && (ToolDelegationDepth(ctx) > 0 || ToolAutoDenyAsk(ctx) || strings.TrimSpace(ToolTranscriptSessionID(ctx)) == "") {
		return ErrDelegatedAvaWrite
	}
	return nil
}
