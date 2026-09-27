// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/shellrule"
)

// ADR-092 D9 §5.7: when the agent loop's one upfront prompt already settled
// a D3 ask rule (WithRuleAskSettled), the bash tool does not prompt again.
// The pin settles only the ask rule: a deny rule still refuses, and a call
// without the pin still prompts.
func TestEnforceShellPermissionMode_RuleAskSettledPin(t *testing.T) {
	resolvedTrue, err := shellrule.ResolveBinary("true", os.Getenv("PATH"))
	require.NoError(t, err)

	t.Run("pinned ask rule runs with no second prompt", func(t *testing.T) {
		tool, ctx, requester, _ := permTestFixture(t, ShellModeAsk, false)
		tool.commandRules = []shellrule.Rule{{Action: shellrule.ActionAsk, Binary: resolvedTrue}}
		perm, result := tool.enforceShellPermissionMode(WithRuleAskSettled(ctx), "true")
		require.Nil(t, result)
		require.NotNil(t, perm)
		assert.Zero(t, requester.callCount())
	})
	t.Run("without the pin the ask rule still prompts", func(t *testing.T) {
		tool, ctx, requester, _ := permTestFixture(t, ShellModeAsk, false)
		tool.commandRules = []shellrule.Rule{{Action: shellrule.ActionAsk, Binary: resolvedTrue}}
		perm, result := tool.enforceShellPermissionMode(ctx, "true")
		require.Nil(t, perm)
		require.NotNil(t, result)
		assert.Equal(t, 1, requester.callCount())
	})
	t.Run("the pin never overrides a deny rule", func(t *testing.T) {
		tool, ctx, requester, _ := permTestFixture(t, ShellModeAsk, true)
		tool.commandRules = []shellrule.Rule{{Action: shellrule.ActionDeny, Binary: resolvedTrue}}
		perm, result := tool.enforceShellPermissionMode(WithRuleAskSettled(ctx), "true")
		require.Nil(t, perm)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Contains(t, result.ForLLM, "operator rule")
		assert.Zero(t, requester.callCount())
	})
	t.Run("absent and nil contexts are not settled", func(t *testing.T) {
		assert.False(t, RuleAskSettled(context.Background()))
		assert.False(t, RuleAskSettled(nil)) //nolint:staticcheck // nil-context safety is the contract under test
	})
}
