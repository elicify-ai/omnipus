package agent

// Order 12 / CHECK F17: S-12, S-13, C-12 and D1#10–11.
// Real task run (TaskExecutor.ExecuteTask, see unattended_task_run_helper_test.go) ->
// model tool call -> real Skill/load authority -> transcript/model request. No
// callback decides whether the skill ran.
// Only the no-paid-call provider and an unexpected-approval observer are
// substituted. The observer rejects any prompt: unattended runs must never ask.
// Missing sandbox.skill_policies is a loud BLOCKED, not a second decision
// implementation. GREEN/mutation proof remains deferred to independent CHECK.
//
// Fix round 4 (team-lead ruling, part B row 8 option 1, part D item 8): the run is a real
// task run, not a cron-job ProcessScheduled turn, because S-12 says "a scheduled task run of
// Mia" and the A sentence below is what the task run gives (a cron job keeps the headless
// sentence, #1263 R4). The refusal is persisted in the denied tool call's Result["text"] (the
// carrier pkg/gateway/task_run_ask_approval_test.go and TestTimedAllowance_Unattended read), so
// that is where the exact sentence is read now; the old read of entry.Content alone could not
// see it. Every other oracle is unchanged.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const unattendedSkillSentinel = "S12-DEEP-RESEARCH-INSTRUCTIONS-9Z"
const unattendedSkillRefusal = "Not run: needs approval, and task and plan runs never ask."

func TestSkillLoad_AskUnattendedRefused(t *testing.T) {
	// Plan/oracles: D1#10 Ask/Auto off refuses; D1#11 Auto settles Ask;
	// S-13 Allow loads without consent. All are granted and trust Allow so
	// neither a grant refusal nor a trust card can masquerade as this gate.
	for _, row := range []struct {
		name, policy string
		auto, loads  bool
	}{
		{"D1#10 Ask Auto off refuses without a card", "ask", false, false},
		{"D1#11 Ask Auto on loads without a card", "ask", true, true},
		{"S-13 granted Allow loads without a card", "allow", false, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(config.EnvHome, home)
			skillDir := filepath.Join(home, "skills", "deep-research")
			require.NoError(t, os.MkdirAll(skillDir, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(
				"---\nname: deep-research\ndescription: Research for the unattended test.\n---\n\n"+unattendedSkillSentinel+"\n"), 0o600))
			granted := persistSkillGrantFixture(t, home, config.AgentConfig{ID: "mia", Skills: []string{"deep-research"}})

			cfg := config.DefaultConfig()
			cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
			cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "unattended-skill-script"}
			cfg.Agents.Defaults.MaxToolIterations = 3
			cfg.Agents.List = []config.AgentConfig{granted}
			cfg.Sandbox.AutoApprove, cfg.Sandbox.GodMode = row.auto, false
			cfg.Sandbox.SkillTrust = config.SkillTrustLevel("allow") // C-5: trust is independent of permission.
			cfg.Sandbox.ToolPolicies["Skill"] = "allow"              // C-0: the infrastructure tool is fixed Allow.
			cfg.Tools.Manifest.Compressed = false                    // Discovery is not this test's subject.
			cfg.Planning.GoalMaxRounds = 1                           // the run ends after its one scripted tool call (runRealUnattendedTask).
			// Literal input through the real config decoder; do not compute an
			// ask_pending/allowed result in a test adapter. The final assertion
			// catches today's ignored/missing map rather than silently certifying it.
			input, err := json.Marshal(map[string]any{"skill_policies": map[string]string{"deep-research": row.policy}})
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(input, &cfg.Sandbox))
			provider := testutil.NewScenario().
				WithToolCall("Skill", `{"name":"deep-research"}`).WithText("done")
			msgBus := bus.NewMessageBus()
			t.Cleanup(msgBus.Close)
			al := mustNewAgentLoop(t, cfg, msgBus, provider)
			t.Cleanup(al.Close)
			installTestSessionLauncher(t, al) // the production task-start wiring: session task id + lifecycle record
			mia, ok := al.GetRegistry().GetAgent("mia")
			require.True(t, ok)
			mia.ContextBuilder.WithSkillAllowlist(granted.Skills).WithSkillBindings(granted.SkillGrants)
			_, resolved := mia.ContextBuilder.ResolveSkillFullForWorkspace("", "deep-research")
			require.True(t, resolved, "fixture: real installed generation is granted, not merely a name")
			approver := &autoRecordingApprover{approve: false}
			al.SetToolApprover(approver)
			sessionID := runRealUnattendedTask(t, al, "mia", "Load deep-research.")
			assert.Empty(t, approver.requests(), "S-12/S-13: no card may be requested")

			requests := provider.AllRequests()
			require.GreaterOrEqual(t, len(requests), 2, "fixture: actual tool result reached a later model request")
			var toolResults []providers.Message
			for _, message := range requests[1] {
				if message.Role == "tool" {
					toolResults = append(toolResults, message)
				}
			}
			require.Len(t, toolResults, 1, "one result for the one actual Skill call")
			entries, err := al.GetSessionStore().ReadTranscript(sessionID)
			require.NoError(t, err)
			var transcript strings.Builder
			var deniedSkillTexts []string
			for _, entry := range entries {
				transcript.WriteString(entry.Content)
				transcript.WriteByte('\n')
				for _, call := range entry.ToolCalls {
					text, ok := call.Result["text"].(string)
					if !ok {
						continue
					}
					transcript.WriteString(text)
					transcript.WriteByte('\n')
					if call.Tool == "Skill" && call.Status == "denied" {
						deniedSkillTexts = append(deniedSkillTexts, text)
					}
				}
			}
			if row.loads {
				assert.Contains(t, toolResults[0].Content, unattendedSkillSentinel, "the real load returned its instructions")
				assert.NotContains(t, transcript.String(), unattendedSkillRefusal, "a successful load is not refused")
			} else {
				assert.NotContains(t, toolResults[0].Content, unattendedSkillSentinel, "refused instructions never reach the model")
				assert.Contains(t, transcript.String(), unattendedSkillRefusal, "S-12: exact transcript refusal, not a fabricated reply")
				assert.Equal(t, []string{unattendedSkillRefusal}, deniedSkillTexts,
					"S-12: the one real persisted denial of the one Skill call carries the exact spec sentence")
				for _, request := range requests {
					encoded, marshalErr := json.Marshal(request)
					require.NoError(t, marshalErr)
					assert.NotContains(t, string(encoded), unattendedSkillSentinel, "no request may contain refused instructions")
				}
			}
			encoded, err := json.Marshal(cfg.Sandbox)
			require.NoError(t, err)
			var stored map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(encoded, &stored))
			if len(stored["skill_policies"]) == 0 {
				t.Fatal("BLOCKED: sandbox.skill_policies is not implemented — required by C-2/C-12, S-12 and D1#10–11; the real scheduled path above does not certify the missing setting")
			}
			var policies map[string]string
			require.NoError(t, json.Unmarshal(stored["skill_policies"], &policies))
			require.Equal(t, row.policy, policies["deep-research"], "the real loop was given the specified raw permission")
		})
	}
}
