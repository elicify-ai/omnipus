//go:build goolm && stdjson

package tools

// Team-lead round 12 (Skills & Tools #1963; after round 11, S-102). A
// project skill's own Deny is worded the same whether the Skill load or the
// folder access was refused, so a person sees one message: it names the
// project skill and its workspace (spec S-102 "naming the Deny of the
// project skill `notes` in W1"; C-12 "the refusal names the setting").
// Fixture: workspace w1 is named "D14 W1"; the project skill is `notes`.

import (
	"encoding/json"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const projectGateDenyText = "The project skill `notes` in workspace \"D14 W1\" is set to Deny in Skills & Tools. Change its permission there to use it."

func TestProjectSkillGate_DenyNamesTheProjectSkillAndWorkspace(t *testing.T) {
	t.Run("folder read refused by the project skill's Deny", func(t *testing.T) {
		f := seedGateFixture(t, ".claude", "notes", "deny", "deny", false)
		assert.Contains(t, deniedGateFields(t, f, f.skill), projectGateDenyText)
	})

	t.Run("a workspace with no name is named by its id", func(t *testing.T) {
		f := seedGateFixture(t, ".claude", "notes", "deny", "deny", false)
		require.NoError(t, workspace.SaveRecord(f.home, workspace.Workspace{ID: "w1", Status: "active", CoreTeam: []string{"mia"}}))
		assert.Contains(t, deniedGateFields(t, f, f.skill),
			"The project skill `notes` in workspace \"w1\" is set to Deny in Skills & Tools. Change its permission there to use it.")
	})

	t.Run("an Ask keeps its own wording", func(t *testing.T) {
		f := seedGateFixture(t, ".claude", "notes", "ask", "deny", false)
		assert.Contains(t, deniedGateFields(t, f, f.skill), gateAskPendingText)
	})
}

// deniedGateFields reads path with the real read_file tool and returns the
// reason and message of the PermissionDenied refusal it must produce; the
// gate's sentence must be one of them, whole.
func deniedGateFields(t *testing.T, f d14NativeFixture, path string) []string {
	t.Helper()
	result := NewReadFileTool(f.agentHome, true, 1<<20).Execute(f.ctx, map[string]any{"path": path})
	require.True(t, result.IsError, "the project skill folder is refused: %s", result.ForLLM)
	var refusal gen.PermissionDenied
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &refusal), "the refusal uses the PermissionDenied carrier: %s", result.ForLLM)
	assert.NotContains(t, result.ForLLM, "GATE-SENTINEL", "no byte of the refused skill reaches the agent")
	t.Logf("observed refusal: %q", result.ForLLM)
	return []string{refusal.Reason, refusal.Message}
}
