// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RC-5 (ADR-057 UAT root-cause fix) regression coverage.
//
// Before this fix, a failed non-delegate tool call (bash, write_file, etc.)
// persisted a session.ToolCall with Status "error" but a nil Result — the
// human-readable failure reason (toolResult.ContentForLLM(), already
// computed for the LLM message and already logged) was silently dropped on
// the durable-transcript write path. The orchestrating agent had no way to
// learn WHY a call failed and would retry blindly. See pkg/agent/loop.go's
// tcRecord construction in runTurn (the generalized `tcRecord.Result == nil`
// branch) for the fix, and pkg/session/daypartition.go's session.ToolCall.
// Error field doc comment for the wire contract.

package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// failingStubTool is a test-only tools.Tool (non-"delegate", produces no
// Media) that always fails with a distinctive, greppable error message —
// the exact shape RC-5 targets: a tool whose failure previously vanished
// from the persisted transcript because neither of the two pre-existing
// Result-populating branches (media descriptors, buildSyncDelegateResult)
// applied to it.
type failingStubTool struct {
	tools.BaseTool
	wasCalled bool
}

func (f *failingStubTool) Name() string        { return "fail_tool" }
func (f *failingStubTool) Description() string { return "RC-5 test stub — always fails" }
func (f *failingStubTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (f *failingStubTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (f *failingStubTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	f.wasCalled = true
	return tools.ErrorResult("rc5_test: disk full while writing output")
}

// TestRunTurn_FailedToolCall_PersistsErrorReasonInTranscript is the RC-5
// regression test. It must FAIL on the pre-fix code (tc.Result and tc.Error
// both empty/nil for a failed non-delegate, non-media tool call) and PASS
// once loop.go's runTurn populates session.ToolCall.Error from
// toolResult.ContentForLLM() whenever no richer Result was already attached.
func TestRunTurn_FailedToolCall_PersistsErrorReasonInTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	provider := testutil.NewScenario().
		WithToolCall("fail_tool", `{}`).
		WithText("acknowledged the failure")

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 10

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })

	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(func() { al.Close() })

	mia := registerAgent(t, al, home, "mia", provider, true)

	stub := &failingStubTool{}
	al.RegisterTool(stub)
	// No-default-policy model (CLAUDE.md hard constraint 6): explicit allow
	// so the scripted "fail_tool" call actually reaches Execute rather than
	// failing closed to "deny" before this test can observe anything.
	mia.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"fail_tool": "allow"},
	})

	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err, "NewScheduledSession must succeed")
	sessionID := meta.ID

	reply, err := al.ProcessScheduled(
		context.Background(),
		"mia", sessionID,
		"please call fail_tool",
		"scheduled", sessionID,
	)
	require.NoError(t, err, "ProcessScheduled must not error — the TOOL fails, the turn itself recovers")
	assert.Equal(t, "acknowledged the failure", reply)
	assert.True(t, stub.wasCalled, "fail_tool must actually have executed")

	store := al.ResolveSessionStore(sessionID)
	require.NotNil(t, store, "ResolveSessionStore must find the session store")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err, "ReadTranscript must succeed")
	require.NotEmpty(t, entries, "transcript must be non-empty")

	var found bool
	var gotToolCalls []session.ToolCall
	for _, e := range entries {
		for _, tc := range e.ToolCalls {
			if tc.Tool != "fail_tool" {
				continue
			}
			gotToolCalls = append(gotToolCalls, tc)
			assert.Equal(t, "error", tc.Status, "failed tool call must persist Status \"error\"")
			assert.NotEmpty(t, tc.Error,
				"RC-5: a failed tool call must persist a non-empty human-readable Error reason "+
					"— before the fix this stayed empty because neither the media nor the "+
					"delegate-result branch applied to a plain failing tool")
			assert.Contains(t, strings.ToLower(tc.Error), "disk full",
				"the persisted Error must carry the actual failure reason "+
					"(the same string sent to the LLM), not a generic placeholder")
			found = true
		}
	}
	require.True(t, found, "the fail_tool call must be present in the persisted transcript; got: %+v", gotToolCalls)
}
