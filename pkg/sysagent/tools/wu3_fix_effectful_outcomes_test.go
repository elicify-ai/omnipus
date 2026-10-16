package systools_test

import (
	"context"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
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
	deps, cfg := newTestDepsWithCredStore(t)
	cfg.Channels = map[string]config.ChannelInstanceConfig{}

	enable := systools.NewChannelEnableTool(deps)
	res := enable.Execute(context.Background(), systools.WithCurrentRevisionForTest(enable, map[string]any{"id": "telegram"}))
	require.False(t, res.IsError, res.ForLLM)
	body := wu3Verdict(t, res, "saved but not working", scope, notTested...)
	assert.Contains(t, body["reason"], "next config reload")
	assert.True(t, cfg.Channels["telegram"].Enabled, "enable is durable")

	configure := systools.NewChannelConfigureTool(deps)
	res = configure.Execute(context.Background(), systools.WithCurrentRevisionForTest(configure,
		map[string]any{"id": "telegram", "token": "bot123:WU3FIXSECRET"}))
	require.False(t, res.IsError, res.ForLLM)
	body = wu3Verdict(t, res, "saved but not working", scope, notTested...)
	assert.Contains(t, body["reason"], "next config reload")
	assert.NotContains(t, res.ForLLM, "WU3FIXSECRET", "the secret is never echoed")
	assert.Equal(t, "channel_telegram_token", cfg.Channels["telegram"].TokenRef, "configure is durable")

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
			reason: "connection failed: connection refused", notTested: live},
		{name: "disconnected", status: "disconnected", outcome: "saved but not working", scope: scope,
			reason: "the server is not connected", notTested: live},
		{name: "reconcile-failed", noStatus: true, reconcileErr: errors.New("boom"), outcome: "saved but not working",
			reason: "live reconciliation failed: boom", notTested: []string{"the live connection"}},
		{name: "unchecked", noStatus: true, noReconcile: true, outcome: "saved but not working",
			reason: "not checked: live connection status is unavailable", notTested: []string{"the live connection"}},
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

// code-reviewer IM-03 / silent-failure-hunter SF-02 / type-design T3: when one
// env credential is stored and a later one fails, the add is "partly saved" —
// the stored credential is a durable part — never "failed: nothing saved".
func TestWU3MCPAddLaterCredentialFailureIsPartlySaved(t *testing.T) {
	deps, cfg := newTestDepsWithCredStore(t)
	systools.FailMCPCredentialWriteAfterForTest(t, 1)
	add := systools.NewMCPAddTool(deps)
	res := add.Execute(context.Background(), systools.WithCurrentRevisionForTest(add, map[string]any{
		"name": "two-secrets", "command": "npx",
		"env": map[string]any{"ALPHA": "wu3-first-value", "BETA": "wu3-second-value"},
	}))
	require.True(t, res.IsError, res.ForLLM)
	body := wu3Outcome(t, res, "partly saved")
	assert.Equal(t, []any{"env_credentials"}, body["changed_fields"], "the stored credential is a durable part")
	stored, ok := body["stored_credentials"].([]any)
	require.True(t, ok, "the stored credential references are named: %s", res.ForLLM)
	require.Len(t, stored, 1)
	ref, _ := stored[0].(string)
	value, err := deps.CredStore.Get(ref)
	require.NoError(t, err, "the named reference really is stored")
	assert.Contains(t, []string{"wu3-first-value", "wu3-second-value"}, value)
	assert.Contains(t, body["missing_parts"], "mcp server entry")
	assert.NotContains(t, res.ForLLM, "wu3-first-value")
	assert.NotContains(t, res.ForLLM, "wu3-second-value")
	_, saved := cfg.Tools.MCP.Servers["two-secrets"]
	assert.False(t, saved, "the server entry was not saved")
}

// Control for the partial-credential case: a first credential write failing
// saves nothing, and is still "failed: nothing saved".
func TestWU3MCPAddFirstCredentialFailureSavesNothing(t *testing.T) {
	deps, cfg := newTestDepsWithCredStore(t)
	systools.FailMCPCredentialWriteAfterForTest(t, 0)
	add := systools.NewMCPAddTool(deps)
	res := add.Execute(context.Background(), systools.WithCurrentRevisionForTest(add, map[string]any{
		"name": "one-secret", "command": "npx", "env": map[string]any{"ALPHA": "wu3-only-value"},
	}))
	require.True(t, res.IsError, res.ForLLM)
	body := wu3Outcome(t, res, "failed: nothing saved")
	assert.Equal(t, []any{}, body["changed_fields"])
	_, err := deps.CredStore.Get("mcp_one-secret_ALPHA")
	require.Error(t, err, "nothing was stored")
	_, saved := cfg.Tools.MCP.Servers["one-secret"]
	assert.False(t, saved)
}

// type-design T3: the config write fails after the env credential was stored
// and the rollback cannot delete it — the result names the durable credential
// in changed_fields/stored_credentials, not only inside the reason text.
func TestWU3MCPAddRollbackFailureNamesDurableCredentials(t *testing.T) {
	deps, cfg := newTestDepsWithCredStore(t)
	// The config save fails, and the rollback's delete of the stored
	// credential fails too (deterministic fault seams; no file permissions,
	// so the test runs the same as root — CHECK R2-03).
	deps.SaveConfigLocked = func(*config.Config) error { return errors.New("injected config save failure") }
	systools.FailMCPCredentialRollbackForTest(t)
	add := systools.NewMCPAddTool(deps)
	res := add.Execute(context.Background(), systools.WithCurrentRevisionForTest(add, map[string]any{
		"name": "rb", "command": "npx", "env": map[string]any{"TOKEN": "wu3-rollback-value"},
	}))
	require.True(t, res.IsError, res.ForLLM)
	body := wu3Outcome(t, res, "partly saved")
	assert.Equal(t, []any{"env_credentials"}, body["changed_fields"])
	assert.Equal(t, []any{"mcp_rb_TOKEN"}, body["stored_credentials"])
	value, err := deps.CredStore.Get("mcp_rb_TOKEN")
	require.NoError(t, err, "the credential the result names really is still stored")
	assert.Equal(t, "wu3-rollback-value", value)
	assert.NotContains(t, res.ForLLM, "wu3-rollback-value")
	_, saved := cfg.Tools.MCP.Servers["rb"]
	assert.False(t, saved)
}
