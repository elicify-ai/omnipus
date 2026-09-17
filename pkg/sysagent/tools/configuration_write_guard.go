package systools

import (
	"context"
	"errors"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

var ErrDelegatedAvaWrite = errors.New("Ava may write configuration only from an attended, user-owned session; switch to Ava in the user-owned session")

// ValidateConfigurationWriteContext is the shared execution guard for every
// configuration mutation tool. Ava may mutate only in an owner session;
// delegated Ava runs (depth > 0) are proposal-only. Other identities retain
// their existing authorization behavior.
func ValidateConfigurationWriteContext(ctx context.Context) error {
	if tools.ToolAgentID(ctx) == "ava" {
		if tools.ToolDelegationDepth(ctx) > 0 || tools.ToolAutoDenyAsk(ctx) || strings.TrimSpace(tools.ToolTranscriptSessionID(ctx)) == "" {
			return ErrDelegatedAvaWrite
		}
	}
	return nil
}
