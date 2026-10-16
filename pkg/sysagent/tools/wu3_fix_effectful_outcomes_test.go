package systools_test

import (
	"context"
	"errors"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wu3Verdict asserts the exact FR-030 verdict fields of a change result.
func wu3Verdict(t *testing.T, result *tools.ToolResult, outcome, scope string, notTested ...string) map[string]any {
	t.Helper()
	body := wu3Outcome(t, result, outcome)
	assert.Equal(t, scope, body["check_scope"], "check_scope states what was actually checked")
	want := make([]any, 0, len(notTested))
	for _, s := range notTested {
		want = append(want, s)
	}
	assert.Equal(t, want, body["not_tested"])
	return body
}

// CHECK F04 / pr-test-analyzer TC-05: a successful channel write is saved but
// never applied live on the tool path, so it is "saved but not working" with
// the reload reason — never "working" (kills CHECK mutant M09).
func TestWU3ChannelSuccessfulWritesAreSavedButNotWorking(t *testing.T) {
	const scope = "saved configuration read back"
	notTested := []string{"authentication with the platform", "the live connection"}
	deps, cfg := wu3Deps(t)
	cfg.Channels = map[string]config.ChannelInstanceConfig{}

	enable := systools.NewChannelEnableTool(deps)
	res := enable.Execute(context.Background(), systools.WithCurrentRevisionForTest(enable, map[string]any{"id": "telegram"}))
	require.False(t, res.IsError, res.ForLLM)
	body := wu3Verdict(t, res, "saved but not working", scope, notTested...)
	assert.Contains(t, body["reason"], "next config reload")
	assert.True(t, cfg.Channels["telegram"].Enabled, "enable is durable")

	ref := systools.IssueCardReferencesForTest(t, deps, secretrequest.Input{Instance: "telegram", Field: "token"}, "bot123:WU3FIXSECRET")[0]
	configure := systools.NewChannelConfigureTool(deps)
	res = configure.Execute(context.Background(), systools.WithCurrentRevisionForTest(configure,
		map[string]any{"id": "telegram", "token_ref": ref}))
	require.False(t, res.IsError, res.ForLLM)
	body = wu3Verdict(t, res, "saved but not working", scope, notTested...)
	assert.Contains(t, body["reason"], "next config reload")
	assert.NotContains(t, res.ForLLM, "WU3FIXSECRET", "the secret is never echoed")
	assert.Equal(t, ref, cfg.Channels["telegram"].TokenRef, "the prior card version is selected durably; plaintext is not a tool input")

	disable := systools.NewChannelDisableTool(deps)
	res = disable.Execute(context.Background(), systools.WithCurrentRevisionForTest(disable, map[string]any{"id": "telegram"}))
	require.False(t, res.IsError, res.ForLLM)
	body = wu3Verdict(t, res, "saved but not working", scope, notTested...)
	assert.Contains(t, body["reason"], "next config reload")
	assert.False(t, cfg.Channels["telegram"].Enabled, "disable is durable")
}

// TC-05: add_mcp_server is "working" only when the live status is connected;
// every other live state, or no live check at all, is "saved but not working".
func TestWU3MCPAddOutcomeFollowsLiveStatus(t *testing.T) {
	// Chief Q4-A: every nonworking secret-free CREATE gets this exact
	// conditional setup explanation, followed by the unchanged actual failure.
	const provisional = "server saved without credential references; credential setup is not finished if this server needs a secret. "
	const scope = "live connection: the server's status after reconciliation and the number of tools it registered"
	live := []string{"calling the server's tools", "tool policies (none are assigned yet)"}
	cases := []struct {
		name, status, errMsg string
		reconcileErr         error
		noStatus             bool
		noReconcile          bool
		outcome, scope       string
		reason               string
		notTested            []string
	}{
		{name: "connected", status: "connected", outcome: "working", scope: scope, notTested: live},
		{name: "error", status: "error", errMsg: "connection refused", outcome: "saved but not working", scope: scope,
			reason: provisional + "connection failed: connection refused", notTested: live},
		{name: "disconnected", status: "disconnected", outcome: "saved but not working", scope: scope,
			reason: provisional + "the server is not connected", notTested: live},
		{name: "reconcile-failed", noStatus: true, reconcileErr: errors.New("boom"), outcome: "saved but not working",
			reason: provisional + "live reconciliation failed: boom", notTested: []string{"the live connection"}},
		{name: "unchecked", noStatus: true, noReconcile: true, outcome: "saved but not working",
			reason: provisional + "not checked: live connection status is unavailable", notTested: []string{"the live connection"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, cfg := newTestDeps(t)
			cfg.Tools.MCP.Enabled = true // the global switch is already on: only the entry changes
			if !tc.noReconcile {
				deps.ReconcileMCP = func(context.Context) error { return tc.reconcileErr }
			}
			if !tc.noStatus {
				deps.MCPStatus = func(string) (string, int, string) { return tc.status, 2, tc.errMsg }
			}
			add := systools.NewMCPAddTool(deps)
			res := add.Execute(context.Background(), systools.WithCurrentRevisionForTest(add,
				map[string]any{"name": "srv-" + tc.name, "command": "npx"}))
			require.False(t, res.IsError, res.ForLLM)
			body := wu3Verdict(t, res, tc.outcome, tc.scope, tc.notTested...)
			if tc.reason != "" {
				assert.Equal(t, tc.reason, body["reason"])
			}
			assert.Equal(t, []any{"mcp_server_entry"}, body["changed_fields"])
			_, saved := cfg.Tools.MCP.Servers["srv-"+tc.name]
			assert.True(t, saved, "the server entry is durable in every case")
		})
	}
}

// TC-05: remove_mcp_server is "working" only after a confirmed live
// disconnect; a failed or unavailable disconnect is "saved but not working",
// and an undeletable stored credential makes it "partly saved".
func TestWU3MCPRemoveOutcomeFollowsLiveDisconnect(t *testing.T) {
	const scope = "live disconnect: reconciliation after the config removal"
	cases := []struct {
		name         string
		reconcile    func(context.Context) error
		envRefs      map[string]string
		outcome      string
		scope        string
		reason       string
		missingParts []any
	}{
		{name: "disconnected", reconcile: func(context.Context) error { return nil }, outcome: "working", scope: scope,
			missingParts: []any{}},
		{name: "disconnect-failed", reconcile: func(context.Context) error { return errors.New("stuck") },
			outcome: "saved but not working", scope: scope, reason: "live disconnect not confirmed: stuck", missingParts: []any{}},
		{name: "unchecked", outcome: "saved but not working", reason: "not checked: live reconciliation is unavailable",
			missingParts: []any{}},
		{name: "credential-cleanup-unavailable", reconcile: func(context.Context) error { return nil },
			envRefs: map[string]string{"TOKEN": "mcp_srv_TOKEN"}, outcome: "partly saved", scope: scope,
			reason:       "the server was removed but some of its stored env credentials could not be deleted",
			missingParts: []any{"env credential deletion"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, cfg := newTestDeps(t) // CredStore nil: cleanup of env refs is impossible
			deps.ReconcileMCP = tc.reconcile
			cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
				"srv": {Enabled: true, Type: "stdio", Command: "npx", EnvRefs: tc.envRefs},
			}
			remove := systools.NewMCPRemoveTool(deps)
			res := remove.Execute(context.Background(), systools.WithCurrentRevisionForTest(remove,
				map[string]any{"name": "srv", "confirm": true}))
			require.False(t, res.IsError, res.ForLLM)
			body := wu3Outcome(t, res, tc.outcome)
			assert.Equal(t, tc.scope, body["check_scope"])
			if tc.reason != "" {
				assert.Equal(t, tc.reason, body["reason"])
			}
			assert.Equal(t, tc.missingParts, body["missing_parts"])
			assert.Equal(t, []any{"mcp_server_entry"}, body["changed_fields"])
			_, still := cfg.Tools.MCP.Servers["srv"]
			assert.False(t, still, "the removal is durable in every case")
		})
	}
}

// D2-A/Q1-A translation of the old later credential-write failure: a valid
// first reference plus a recipient-mismatched later reference saves NOTHING.
// add_mcp_server no longer owns a plaintext value-write/rollback path.
func TestWU4B_MCPLaterRecipientFailureIsFailedNothingSaved(t *testing.T) {
	f := newWU4BFixture(t)
	tool, args, valid := wu4bMatchingSelection(t, f, "add_mcp_server")
	wrong := f.bound(t, credentials.CardRecipient{Kind: "mcp_env", Server: "fixture", Transport: "stdio", Command: "fixture-command", Args: []string{"--read-only"}, Field: "OTHER"}, "wu3-second-value")
	args["env_refs"] = map[string]any{"TOKEN": valid, "ZZZ_BAD": wrong}
	before := f.snapshot(t)
	res := tool.Execute(context.Background(), args)
	f.unchanged(t, before)
	wu4bRefusal(t, res, "recipient")
	assert.NotContains(t, res.ForLLM, "wu3-second-value")
}

// Q1-A translation of the first credential-write failure: the first missing
// reference refuses before any config selection, value write or runtime call.
func TestWU4B_MCPFirstMissingReferenceSavesNothing(t *testing.T) {
	f := newWU4BFixture(t)
	tool, args, _ := wu4bMatchingSelection(t, f, "add_mcp_server")
	args["env_refs"] = map[string]any{"TOKEN": "missing-reference.v1"}
	before := f.snapshot(t)
	res := tool.Execute(context.Background(), args)
	f.unchanged(t, before)
	wu4bRefusal(t, res, "ref")
}

// Q1-A translation of rollback failure: Submit is a PRIOR successful storage
// operation. A later config-save failure cannot delete its values/bindings or
// claim this failed attachment partly saved credentials it never wrote.
func TestWU4B_MCPConfigFailureAfterCardSubmitRetainsValuesWithoutSelection(t *testing.T) {
	f := newWU4BFixture(t)
	tool, args, _ := wu4bMatchingSelection(t, f, "add_mcp_server")
	reg, sid, _ := wu4bCardRegistry(t, f)
	card, err := reg.Create(context.Background(), sid, "omnipus", "wu4b-webchat", "", secretrequest.Input{Purpose: "MCP credential before failing selection", Server: "fixture", EnvKey: "TOKEN"})
	require.NoError(t, err)
	values := map[string]string{"value": "wu3-rollback-value"}
	submitted, err := reg.Submit(card.Card.RequestId, "wu4b-owner", gen.SecretSubmitRequest{Values: &values})
	require.NoError(t, err)
	require.Len(t, submitted.Card.ReferenceNames, 1)
	ref := submitted.Card.ReferenceNames[0]
	args["env_refs"] = map[string]any{"TOKEN": ref}
	before := f.snapshot(t)
	attempts := 0
	f.d.SaveConfigLocked = func(*config.Config) error { attempts++; return errors.New("injected config save failure") }
	res := tool.Execute(context.Background(), args)
	assert.Equal(t, 1, attempts, "instrument: valid selection reached the injected config-save failure")
	f.unchanged(t, before)
	require.True(t, res.IsError, res.ForLLM)
	body := wu3Outcome(t, res, "failed: nothing saved")
	assert.Equal(t, []any{}, body["changed_fields"])
	assert.Contains(t, res.ForLLM, "injected config save failure")
	value, err := f.d.CredStore.Get(ref)
	require.NoError(t, err)
	assert.Equal(t, "wu3-rollback-value", value)
	assert.Empty(t, f.cfg.Tools.MCP.Servers["fixture"].EnvRefs)
	assert.NotContains(t, res.ForLLM, "wu3-rollback-value")
}
