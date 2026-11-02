package agent

// UAT-1 F-10 (skills spec r15 §10.1 item 3): with edit_skill denied, a
// write_file into a skill folder is refused with reason skill_edit_denied, in
// the payload the model reads AND in the audit entry. The UAT found only a
// generic tool_call error and no skill_edit_denied entry.
//
// Real turn: a scripted provider calls write_file against an installed, granted
// skill; the policy is write_file allow, edit_skill deny; the audit file is
// read back from the home.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunTurn_WriteIntoSkillWithEditSkillDenied_ReasonIsSkillEditDenied(t *testing.T) {
	cfg, tmpHome := baseLoopDenialTestConfig(t)
	t.Setenv(config.EnvHome, tmpHome)
	t.Setenv(config.EnvBuiltinSkills, t.TempDir())
	dir := menuSkill(t, filepath.Join(tmpHome, "skills"), "foo", "f10 fixture", menuG1)
	notes := filepath.Join(dir, "notes.md")
	require.NoError(t, os.WriteFile(notes, []byte("KEEP\n"), 0o644))
	mia := cfg.Agents.List[0]
	mia.Skills = []string{"foo"}
	cfg.Agents.List[0] = persistSkillGrantFixture(t, tmpHome, mia)

	args, err := json.Marshal(map[string]any{"path": notes, "content": "CHANGED\n", "overwrite": true})
	require.NoError(t, err)
	provider := testutil.NewScenario().
		WithToolCall("write_file", string(args)).
		WithText("understood")
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	defer al.Close()
	for _, id := range al.GetRegistry().ListAgentIDs() {
		inst, ok := al.GetRegistry().GetAgent(id)
		require.True(t, ok)
		inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{
			"write_file": config.ToolPolicyAllow,
			"edit_skill": config.ToolPolicyDeny,
		}})
	}

	final, err := al.processDirectPrimaryForTest(context.Background(), "update my skill notes", "test-session-f10")
	require.NoError(t, err)
	assert.Equal(t, "understood", final)

	raw, err := os.ReadFile(notes)
	require.NoError(t, err)
	assert.Equal(t, "KEEP\n", string(raw), "instrument: the denied write changed nothing")

	// The result the model read.
	mia0 := al.GetRegistry().primaryChatAgentForTest()
	require.NotNil(t, mia0)
	var denyContent string
	for _, msg := range mia0.Sessions.GetHistory(directTurnHistoryKeyForTest("mia", "cli", "direct")) {
		if msg.Role == "tool" && strings.Contains(msg.Content, "permission_denied") {
			denyContent = msg.Content
			break
		}
	}
	require.NotEmpty(t, denyContent, "the denied write is persisted as a permission_denied tool result")
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(denyContent), &payload))
	assert.Equal(t, "skill_edit_denied", payload["reason"], "the result names the skill-specific reason")

	// The audit entry.
	entries, readErr := readAuditEntries(filepath.Join(tmpHome, "system", "audit.jsonl"))
	require.NoError(t, readErr)
	found := false
	for _, e := range entries {
		if blob, mErr := json.Marshal(e); mErr == nil && strings.Contains(string(blob), "skill_edit_denied") {
			found = true
		}
	}
	assert.True(t, found, "an audit entry carries the reason skill_edit_denied; entries: %v", entries)
}
