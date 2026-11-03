package agent

// UAT-1 F-10 and UAT run 2 R2-1 (skills spec r15 §10.1 item 3, spec line 888):
// with edit_skill denied, a write into the skills tree is refused with reason
// skill_edit_denied in the result the model reads AND in an audit entry; the
// refusal names the skill; the audit entry carries no written content.
//
// Real turn: a scripted provider calls the tool; the policy is the tool
// allowed and edit_skill denied; the audit file is read back from the home.
//
// Run 2 found zero skill_edit_denied entries: the write to an absent or an
// ungranted skill was refused earlier by the path policy ("protected
// carve-out") and the loop's composition step never ran. The granted existing
// skill case already passes (guard); the other cases are the RED part.

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

const f10Content = "CONTENT-MARKER-DO-NOT-LOG"

type f10Case struct {
	name  string
	skill string // the skill the write targets
	tool  string
	args  func(skillsDir string) map[string]any
}

func TestRunTurn_WriteIntoSkillWithEditSkillDenied_ReasonIsSkillEditDenied(t *testing.T) {
	cases := []f10Case{
		{
			name: "write_file into a granted existing skill", skill: "foo", tool: "write_file",
			args: func(d string) map[string]any {
				return map[string]any{"path": filepath.Join(d, "foo", "notes.md"), "content": f10Content, "overwrite": true}
			},
		},
		{
			name: "write_file creating a new skill", skill: "a-test", tool: "write_file",
			args: func(d string) map[string]any {
				return map[string]any{"path": filepath.Join(d, "a-test", "SKILL.md"), "content": f10Content}
			},
		},
		{
			name: "write_file into an installed skill the agent is not granted", skill: "other", tool: "write_file",
			args: func(d string) map[string]any {
				return map[string]any{"path": filepath.Join(d, "other", "notes.md"), "content": f10Content, "overwrite": true}
			},
		},
		{
			name: "bash redirect into a granted skill", skill: "foo", tool: "bash",
			args: func(d string) map[string]any {
				return map[string]any{"command": "echo " + f10Content + " > " + filepath.Join(d, "foo", "notes.md")}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, tmpHome := baseLoopDenialTestConfig(t)
			t.Setenv(config.EnvHome, tmpHome)
			t.Setenv(config.EnvBuiltinSkills, t.TempDir())
			skillsDir := filepath.Join(tmpHome, "skills")
			foo := menuSkill(t, skillsDir, "foo", "f10 fixture", menuG1)
			require.NoError(t, os.WriteFile(filepath.Join(foo, "notes.md"), []byte("KEEP\n"), 0o644))
			other := menuSkill(t, skillsDir, "other", "f10 other", "22222222-2222-2222-2222-222222222222")
			require.NoError(t, os.WriteFile(filepath.Join(other, "notes.md"), []byte("KEEP\n"), 0o644))
			mia := cfg.Agents.List[0]
			mia.Skills = []string{"foo"}
			cfg.Agents.List[0] = persistSkillGrantFixture(t, tmpHome, mia)

			args, err := json.Marshal(tc.args(skillsDir))
			require.NoError(t, err)
			provider := testutil.NewScenario().WithToolCall(tc.tool, string(args)).WithText("understood")
			al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
			defer al.Close()
			for _, id := range al.GetRegistry().ListAgentIDs() {
				inst, ok := al.GetRegistry().GetAgent(id)
				require.True(t, ok)
				inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{
					tc.tool:      config.ToolPolicyAllow,
					"edit_skill": config.ToolPolicyDeny,
				}})
			}

			final, err := al.processDirectPrimaryForTest(context.Background(), "change my skill", "test-session-f10")
			require.NoError(t, err)
			assert.Equal(t, "understood", final)

			// Instrument: nothing was written.
			for _, p := range []string{filepath.Join(skillsDir, "foo", "notes.md"), filepath.Join(skillsDir, "other", "notes.md")} {
				raw, readErr := os.ReadFile(p)
				require.NoError(t, readErr)
				require.Equal(t, "KEEP\n", string(raw), "the denied write changed nothing: %s", p)
			}
			_, statErr := os.Stat(filepath.Join(skillsDir, "a-test"))
			require.ErrorIs(t, statErr, os.ErrNotExist, "no skill was created")

			// The result the model read: a denial whose reason is skill_edit_denied
			// and whose text names the skill.
			agentInst := al.GetRegistry().primaryChatAgentForTest()
			require.NotNil(t, agentInst)
			var denyContent string
			for _, msg := range agentInst.Sessions.GetHistory(directTurnHistoryKeyForTest("mia", "cli", "direct")) {
				if msg.Role == "tool" && msg.Content != "" {
					denyContent = msg.Content
					break
				}
			}
			require.NotEmpty(t, denyContent, "the refused call is persisted as a tool result")
			assert.Contains(t, denyContent, "skill_edit_denied", "the result carries the skill-specific reason")
			assert.Contains(t, denyContent, tc.skill, "the refusal names the skill")
			assert.NotContains(t, denyContent, f10Content)

			// The audit entry.
			entries, readErr := readAuditEntries(filepath.Join(tmpHome, "system", "audit.jsonl"))
			require.NoError(t, readErr)
			found := false
			for _, e := range entries {
				blob, mErr := json.Marshal(e)
				require.NoError(t, mErr)
				if strings.Contains(string(blob), "skill_edit_denied") {
					found = true
					assert.NotContains(t, string(blob), f10Content, "the audit entry carries no written content")
				}
			}
			assert.True(t, found, "an audit entry carries the reason skill_edit_denied; entries: %v", entries)
		})
	}
}
