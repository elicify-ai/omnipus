package agent

// UAT 2026-09-13 D-84: a policy deny that bites at tool-LOAD time (ToolSearch)
// must leave a deny row naming the tool, exactly like a dispatch-time deny.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestToolSearchLoad_PolicyDenyIsAudited(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	const lazyTool = "find_skills"
	scenario := testutil.NewScenario().
		WithToolCall("ToolSearch", `{"names":["find_skills"]}`).
		WithText("that tool is denied for me; reporting the blocker")

	cfg := newE2ECfg(t, workspaceDir)
	cfg.Sandbox.AuditLog = true
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), scenario)
	// Deny ONLY the lazy tool, keeping every seeded entry (ToolSearch itself
	// must stay callable, or the load never happens and nothing is exercised).
	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		inst, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		merged := &tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{}}
		if cur := inst.LoadToolPolicy(); cur != nil {
			*merged = *cur
			merged.Policies = make(map[string]config.ToolPolicy, len(cur.Policies)+1)
			for k, v := range cur.Policies {
				merged.Policies[k] = v
			}
		}
		merged.Policies[lazyTool] = config.ToolPolicyDeny
		inst.StoreToolPolicy(merged)
	}

	reply, err := al.ProcessDirectWithChannel(context.Background(),
		"please load find_skills", "e2e-load-tool-deny-audit", "cli", "test")
	require.NoError(t, err, "reply=%q", reply)
	al.Close()

	data, err := os.ReadFile(filepath.Join(tmpHome, "system", "audit.jsonl"))
	require.NoError(t, err, "the audit log must exist")

	var found bool
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e map[string]any
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e["event"] == "tool.policy.deny.attempted" && e["tool"] == lazyTool {
			found = true
			assert.Equal(t, "deny", e["decision"])
			details, _ := e["details"].(map[string]any)
			assert.Equal(t, "tool_load", details["context"])
			assert.NotEmpty(t, e["agent_id"])
		}
	}
	assert.True(t, found, "a deny row naming %s must exist for a policy-refused load; log was:\n%s", lazyTool, string(data))
}
