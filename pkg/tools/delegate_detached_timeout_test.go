package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

type detachedTimeoutSpawner struct{}

func (detachedTimeoutSpawner) SpawnSubTurn(context.Context, SubTurnConfig) (*ToolResult, error) {
	err := fmt.Errorf("%w: test child ignored cancellation", ErrDelegationDetached)
	return ErrorResult("detached child timeout").WithError(err), err
}

func TestErrDelegationDetachedImpliesTimedOut(t *testing.T) {
	require.ErrorIs(t, ErrDelegationDetached, ErrDelegationTimedOut,
		"every detached delegation must structurally retain ordinary timeout classification")
}

// TestDelegateSync_DetachedTimeoutReportsStillUnwinding exercises the real
// DelegateTool result and lifecycle path. It prevents the tool from replacing
// a truthful detached-child result with the stronger, false claim that an
// already-running cancellation-ignoring tool is fully stopped.
func TestDelegateSync_DetachedTimeoutReportsStillUnwinding(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetDelegationDenyCheckerAwait(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	lifecycle := session.NewLifecycleStore(t.TempDir())
	tool.SetLifecycleStore(lifecycle)
	tool.SetSpawner(detachedTimeoutSpawner{})

	ctx := WithTranscriptSessionID(WithAgentID(context.Background(), "issue-769-lead"), "issue-769-parent")
	result := tool.Execute(ctx, map[string]any{
		"task": "enter a cancellation-ignoring tool", "label": "stuck worker", "async": false,
	})

	require.NotNil(t, result)
	require.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "ignored cancellation")
	assert.Contains(t, result.ForLLM, "parent stopped waiting")
	assert.Contains(t, result.ForLLM, "already-running operation may still be unwinding")
	assert.NotContains(t, result.ForLLM, "nothing left to cancel")
	assert.NotContains(t, result.ForLLM, "will make no further tool calls or file changes")

	var childSessionID string
	for _, line := range strings.Split(result.ForLLM, "\n") {
		if strings.HasPrefix(line, "Session: ") {
			childSessionID = strings.TrimPrefix(line, "Session: ")
			break
		}
	}
	require.NotEmpty(t, childSessionID, "delegator-facing timeout must identify the detached child session")
	record, err := lifecycle.Load(childSessionID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleTimedOut, record.State)
}
