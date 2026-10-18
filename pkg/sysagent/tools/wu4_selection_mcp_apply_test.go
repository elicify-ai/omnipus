package systools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SF-04 / FR-030, FR-064: configuration publication and live application are
// separate facts. The runtime process-edge fixture independently tracks the
// connected reference. It deliberately refuses before replacing v1, not after
// applying v2. The real tool/save/revision/store/result paths remain under test.
func TestWU4Selection_MCPFailedReplacementCannotUseOldConnectedStatusAsProof(t *testing.T) {
	f, tool, oldRef, newRef := selectionMCPReplacement(t)
	liveReference := oldRef
	statusReads := 0
	f.d.ReconcileMCP = func(ctx context.Context) error {
		f.unlocked(t)
		f.reconciles++
		require.NoError(t, ctx.Err(), "fault is inside reconciliation after the configuration save")
		require.Equal(t, newRef, f.cfg.Tools.MCP.Servers["fixture"].EnvRefs["TOKEN"])
		require.Equal(t, oldRef, liveReference, "runtime retains the original connection until application")
		return context.DeadlineExceeded // deterministic pre-application cut; no 20-second sleep
	}
	f.d.MCPStatus = func(name string) (string, int, string) {
		require.Equal(t, "fixture", name)
		statusReads++
		require.Equal(t, oldRef, liveReference, "connected is still v1, never attestation for saved v2")
		return "connected", 2, ""
	}
	before := f.snapshot(t)
	revision := wu3SharedRow(t, f.d, "mcp", "fixture")["revision"]
	statusReads = 0
	result := tool.Execute(context.Background(), map[string]any{
		"name": "fixture", "env_refs": map[string]any{"TOKEN": newRef}, "expected_revision": revision,
	})
	body := wu3Success(t, result)
	assert.Equal(t, "saved but not working", body["outcome"], "SF-04: failure before apply cannot be promoted to working by v1's connected status")
	reason, _ := body["reason"].(string)
	assert.Contains(t, reason, "context deadline exceeded", "preserve the safe reconciliation failure reason")
	assert.NotContains(t, reason, "credential setup is not finished", "Q4 prefix belongs only to secret-free CREATE")
	assert.Equal(t, []any{"env_refs"}, body["changed_fields"], "the reference save succeeded even though application did not")
	assert.Equal(t, newRef, f.cfg.Tools.MCP.Servers["fixture"].EnvRefs["TOKEN"])
	assert.Equal(t, oldRef, liveReference)
	assert.Equal(t, 1, f.reconciles)
	assert.LessOrEqual(t, statusReads, 1, "an optional old-status read is not independent saved-revision proof")
	savedRevision := wu3SharedRow(t, f.d, "mcp", "fixture")["revision"]
	assert.NotEqual(t, revision, savedRevision)
	assert.Equal(t, savedRevision, body["revision"], "return the retained saved revision, not the old runtime revision")
	selectionAssertMCPStored(t, f, oldRef, newRef, before)
	for _, value := range []string{"FAKE-SELECTION-MCP-V1", "FAKE-SELECTION-MCP-V2"} {
		assert.NotContains(t, result.ForLLM, value, "neither stored value belongs in the result")
	}
}

func TestWU4Selection_MCPSuccessfulApplyAndIdenticalAttachmentControls(t *testing.T) {
	f, tool, oldRef, newRef := selectionMCPReplacement(t)
	liveReference := oldRef
	f.d.ReconcileMCP = func(context.Context) error {
		f.unlocked(t)
		f.reconciles++
		liveReference = f.cfg.Tools.MCP.Servers["fixture"].EnvRefs["TOKEN"]
		return nil
	}
	f.d.MCPStatus = func(string) (string, int, string) {
		if f.reconciles == 0 {
			require.Equal(t, oldRef, liveReference, "initial public revision read still observes v1")
		} else {
			require.Equal(t, newRef, liveReference, "successful control reports connectivity only after applying selected v2")
		}
		return "connected", 2, ""
	}
	before := f.snapshot(t)
	args := map[string]any{"name": "fixture", "env_refs": map[string]any{"TOKEN": newRef},
		"expected_revision": wu3SharedRow(t, f.d, "mcp", "fixture")["revision"]}
	result := tool.Execute(context.Background(), args)
	body := wu3Outcome(t, result, "working")
	assert.False(t, result.IsError)
	assert.Equal(t, "connected", body["status"])
	assert.Equal(t, float64(2), body["tool_count"])
	assert.Equal(t, newRef, liveReference)
	assert.Equal(t, 1, f.reconciles)
	selectionAssertMCPStored(t, f, oldRef, newRef, before)

	unchanged := f.snapshot(t)
	args["expected_revision"] = wu3SharedRow(t, f.d, "mcp", "fixture")["revision"]
	noOp := tool.Execute(context.Background(), args)
	noOpBody := wu3Outcome(t, noOp, "working")
	assert.False(t, noOp.IsError)
	assert.Equal(t, []any{}, noOpBody["changed_fields"])
	assert.Contains(t, noOpBody["note"], "nothing was changed")
	assert.NotContains(t, noOp.ForLLM, "credential setup is not finished")
	f.unchanged(t, unchanged)
}

func TestWU4Selection_MCPProvisionalPrefixStaysCreateOnly(t *testing.T) {
	const prefix = "server saved without credential references; credential setup is not finished if this server needs a secret. "
	t.Run("secret_free_create", func(t *testing.T) {
		f := newWU4BFixture(t)
		f.d.MCPStatus = func(string) (string, int, string) { return "disconnected", 0, "" }
		result := systools.NewMCPAddTool(f.d).Execute(context.Background(), map[string]any{
			"name": "selection-create", "command": "never-started-fixture", "expected_revision": "absent",
		})
		body := wu3Outcome(t, result, "saved but not working")
		assert.Equal(t, prefix+"the server is not connected", body["reason"], "Q4-A keeps its conditional prefix and original reason")
	})
	t.Run("attachment", func(t *testing.T) {
		f, tool, _, newRef := selectionMCPReplacement(t)
		f.d.MCPStatus = func(string) (string, int, string) { return "disconnected", 0, "" }
		result := tool.Execute(context.Background(), systools.WithCurrentRevisionForTest(tool, map[string]any{
			"name": "fixture", "env_refs": map[string]any{"TOKEN": newRef},
		}))
		body := wu3Outcome(t, result, "saved but not working")
		assert.Equal(t, "the server is not connected", body["reason"], "attachment must not inherit the CREATE prefix")
		assert.NotContains(t, result.ForLLM, prefix)
	})
}

func selectionMCPReplacement(t *testing.T) (*wu4bFixture, tools.Tool, string, string) {
	t.Helper()
	f := newWU4BFixture(t)
	f.cfg.Tools.MCP.Enabled = true
	f.cfg.Tools.MCP.Servers["fixture"] = config.MCPServerConfig{
		Type: "stdio", Command: "fixture-command", Args: []string{"--read-only"}, Enabled: true,
	}
	recipient := credentials.CardRecipient{Kind: "mcp_env", Server: "fixture", Transport: "stdio",
		Command: "fixture-command", Args: []string{"--read-only"}, Field: "TOKEN"}
	oldRef := f.bound(t, recipient, "FAKE-SELECTION-MCP-V1")
	newRef := f.bound(t, recipient, "FAKE-SELECTION-MCP-V2")
	require.NotEqual(t, oldRef, newRef, "replacement is a distinct saved version")
	server := f.cfg.Tools.MCP.Servers["fixture"]
	server.EnvRefs = map[string]string{"TOKEN": oldRef}
	f.cfg.Tools.MCP.Servers["fixture"] = server
	f.persist(t)
	return f, systools.NewMCPAddTool(f.d), oldRef, newRef
}

func selectionAssertMCPStored(t *testing.T, f *wu4bFixture, oldRef, newRef string, before wu4bSnapshot) {
	t.Helper()
	after := f.snapshot(t)
	assert.Equal(t, before.saves+1, after.saves)
	assert.Equal(t, before.credentials, after.credentials, "selection cannot rewrite any stored value/binding/receipt")
	assert.Zero(t, f.reloads)
	assert.Zero(t, f.listings)
	var disk config.Config
	require.NoError(t, json.Unmarshal(after.config, &disk))
	assert.Equal(t, newRef, disk.Tools.MCP.Servers["fixture"].EnvRefs["TOKEN"])
	assert.True(t, disk.Tools.MCP.Enabled)
	assert.True(t, disk.Tools.MCP.Servers["fixture"].Enabled)
	for ref, expected := range map[string]string{oldRef: "FAKE-SELECTION-MCP-V1", newRef: "FAKE-SELECTION-MCP-V2"} {
		value, err := f.d.CredStore.Get(ref)
		require.NoError(t, err)
		assert.Equal(t, expected, value)
	}
}
