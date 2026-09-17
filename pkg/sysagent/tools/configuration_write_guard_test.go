package systools_test

import (
	"context"
	"errors"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestValidateConfigurationWriteContextDelegatedAvaIsProposalOnly(t *testing.T) {
	ctx := tools.WithDelegationDepth(tools.WithAgentID(context.Background(), "ava"), 1)
	if err := systools.ValidateConfigurationWriteContext(ctx); !errors.Is(err, systools.ErrDelegatedAvaWrite) {
		t.Fatalf("error=%v", err)
	}
}

func TestValidateConfigurationWriteContextRejectsUnattendedOrUnownedAva(t *testing.T) {
	for _, ctx := range []context.Context{
		tools.WithAgentID(context.Background(), "ava"),
		tools.WithAutoDenyAsk(tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), "ava"), "owner-session"), true),
	} {
		if err := systools.ValidateConfigurationWriteContext(ctx); !errors.Is(err, systools.ErrDelegatedAvaWrite) {
			t.Fatalf("error=%v", err)
		}
	}
}

func TestValidateConfigurationWriteContextAllowsAttendedOwnerAvaAndOtherDelegates(t *testing.T) {
	for _, ctx := range []context.Context{
		tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), "ava"), "owner-session"),
		tools.WithDelegationDepth(tools.WithAgentID(context.Background(), "jim"), 1),
	} {
		if err := systools.ValidateConfigurationWriteContext(ctx); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}
