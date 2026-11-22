//go:build goolm && stdjson

package agent

// Order 61 / CHECK F17: S-123, C-26 points 2, 3, 5 and 6, D17#13.
// Plan: run real ProcessScheduled -> real Ask/standing-grant decision -> real
// delete_task -> actual task files, later model request and stored transcript.
// Only the scripted no-paid-call provider and an unexpected-card observer are
// substituted. No callback decides whether execution is allowed or returns
// refusal text. Every task is genuinely assigned to the executing agent.
//
// The legacy Record calls below expose the real store's agent/session/argument
// boundary defects. They do NOT constitute a timed card answer. S-123's real
// HTTP allow_for answer, injected monotonic clock and one-hour end are absent
// and stay separately BLOCKED. GREEN reconciles Record's call shape with the
// C-26 store; no expectation may be weakened or a legacy grant called timed.
// Mutation targets (independent CHECK): skip grant lookup, run without consent,
// share Mia's grant with Jim, fingerprint ordinary arguments, bypass Deny, emit
// an approval for an unattended call, or report deletion without deleting.
// GREEN, mutation and complete S-123 acceptance are deferred to CHECK.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimedAllowance_Unattended(t *testing.T) {
	t.Run("control: fixed unattended denial classification", func(t *testing.T) {
		class, ok := ClassifyDenial(autoDenyTaskPlanReason)
		require.True(t, ok)
		require.True(t, class.Permanent, "S-123: the call cannot wait or retry for consent")
		require.Equal(t, "Not run: needs approval, and task and plan runs never ask.", class.TranscriptText)
	})

	rows := []struct {
		name, global, own, actor, grantAgent   string
		grant, sameSession, otherArgs, deleted bool
	}{
		{name: "S-123 no allowance refuses without a card", global: "ask", actor: "mia"},
		{name: "instrument Allow executes the actual task tool", global: "allow", actor: "mia", deleted: true},
		{name: "instrument same-session real grant releases the actual task tool", global: "ask", actor: "mia", grantAgent: "mia", grant: true, sameSession: true, deleted: true},
		{name: "C-26 Mia's other-chat grant reaches a fresh scheduled run", global: "ask", actor: "mia", grantAgent: "mia", grant: true, deleted: true},
		{name: "D17#13 ordinary whole-tool grant covers different task arguments", global: "ask", actor: "mia", grantAgent: "mia", grant: true, sameSession: true, otherArgs: true, deleted: true},
		{name: "C-26 Mia's grant never covers Jim", global: "ask", actor: "jim", grantAgent: "mia", grant: true, sameSession: true},
		{name: "C-26 global Deny refuses even an existing grant", global: "deny", actor: "mia", grantAgent: "mia", grant: true, sameSession: true},
		{name: "C-26 agent's own Ask can be answered from another chat", global: "allow", own: "ask", actor: "mia", grantAgent: "mia", grant: true, deleted: true},
		{name: "C-26 agent's own Deny refuses even an existing grant", global: "allow", own: "deny", actor: "mia", grantAgent: "mia", grant: true, sameSession: true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(config.EnvHome, home)
			cfg := config.DefaultConfig()
			cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
			cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "unattended-task-script"}
			cfg.Agents.Defaults.MaxToolIterations = 3
			cfg.Agents.List = []config.AgentConfig{{ID: "mia", Name: "Mia"}, {ID: "jim", Name: "Jim"}}
			cfg.Sandbox.AutoApprove, cfg.Sandbox.GodMode = false, false
			cfg.Sandbox.ToolPolicies["delete_task"] = row.global
			cfg.Tools.Manifest.Compressed = false // Discovery is not the consent boundary.
			args := map[string]any{"task_id": "s123-target-task"}
			encodedArgs, err := json.Marshal(args)
			require.NoError(t, err)
			provider := testutil.NewScenario().WithToolCall("delete_task", string(encodedArgs)).WithText("done")
			msgBus := bus.NewMessageBus()
			t.Cleanup(msgBus.Close)
			al := mustNewAgentLoop(t, cfg, msgBus, provider)
			t.Cleanup(al.Close)
			actor, ok := al.GetRegistry().GetAgent(row.actor)
			require.True(t, ok)
			_, registered := actor.Tools.Get("delete_task")
			require.True(t, registered, "fixture: actual production deletion tool is registered")
			own := map[string]config.ToolPolicy{}
			if row.own != "" {
				own["delete_task"] = config.ToolPolicy(row.own)
			}
			actor.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{"delete_task": config.ToolPolicy(row.global)}, Policies: own})
			approver := &autoRecordingApprover{approve: false}
			al.SetToolApprover(approver)
			store := GetTaskStore(al)
			require.NotNil(t, store)
			target := &task.Task{ID: "s123-target-task", Title: "Real unattended deletion target", Action: task.ActionLLM, AgentID: row.actor, WorkspaceID: testHarnessWorkspaceMembershipID}
			require.NoError(t, store.CreateByAgent(target, row.actor))
			path := filepath.Join(store.Dir(), target.ID+".json")
			before, err := os.ReadFile(path)
			require.NoError(t, err, "instrument: actual target exists before execution")
			meta, err := al.GetSessionStore().NewScheduledSession(row.actor)
			require.NoError(t, err)
			if row.grant {
				sourceSession := meta.ID
				if !row.sameSession {
					sourceSession = "other-interactive-chat"
				}
				recordedArgs := args
				if row.otherArgs {
					recordedArgs = map[string]any{"task_id": "other-task-argument-A"}
				}
				require.True(t, al.ApprovalGrants().Record(sourceSession, row.grantAgent, "delete_task", recordedArgs), "instrument: real legacy grant recorded; no timed-answer claim")
				require.True(t, al.ApprovalGrants().IsAllowed(sourceSession, row.grantAgent, "delete_task", recordedArgs), "instrument: store lookup sees the record before the actual run")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err = al.ProcessScheduled(ctx, row.actor, meta.ID, "Delete the assigned target task.", "scheduled", meta.ID)
			require.NoError(t, err, "the actual scheduled turn completes")
			require.NoError(t, ctx.Err(), "a deadline is a fixture failure, not proof of refusal")
			assert.Empty(t, approver.requests(), "S-123: unattended turns never raise a card")
			requests := provider.AllRequests()
			require.GreaterOrEqual(t, len(requests), 2, "instrument: actual tool result reaches a later model request")
			var results []providers.Message
			for _, message := range requests[1] {
				if message.Role == "tool" {
					results = append(results, message)
				}
			}
			require.Len(t, results, 1, "one observed tool result for the one scripted call")
			entries, err := al.GetSessionStore().ReadTranscript(meta.ID)
			require.NoError(t, err)
			var transcript strings.Builder
			var deniedTexts []string
			for _, entry := range entries {
				transcript.WriteString(entry.Content)
				transcript.WriteByte('\n')
				for _, call := range entry.ToolCalls {
					if call.Tool == "delete_task" && call.Status == "denied" {
						text, ok := call.Result["text"].(string)
						require.True(t, ok, "actual persisted denial carries display text")
						deniedTexts = append(deniedTexts, text)
						transcript.WriteString(text)
					}
				}
			}
			_, getErr := store.Get(target.ID)
			if row.deleted {
				assert.ErrorIs(t, getErr, task.ErrNotFound, "C-26: released scheduled call must really delete the task, not merely report consent")
				_, statErr := os.Stat(path)
				assert.ErrorIs(t, statErr, os.ErrNotExist, "actual persisted task file must be gone")
				assert.NotContains(t, transcript.String(), unattendedSkillRefusal, "a covered call is not refused")
			} else {
				require.NoError(t, getErr, "a refused task is never deleted")
				after, readErr := os.ReadFile(path)
				require.NoError(t, readErr)
				assert.Equal(t, before, after, "a refused call never rewrites the task")
				if row.global == "deny" || row.own == "deny" {
					assert.Contains(t, strings.ToLower(results[0].Content), "den", "Deny refusal is visible to the model, not an unrelated tool error")
				} else {
					assert.Equal(t, []string{"Not run: needs approval, and task and plan runs never ask."}, deniedTexts, "S-123: the one real persisted denial must use the exact spec sentence")
				}
			}
		})
	}

	t.Run("S-123 actual timed answer and controlled one-hour expiry", func(t *testing.T) {
		t.Fatal("BLOCKED: C-26 duration-bearing store recording and an injected monotonic/wall clock are not implemented — required by S-123; legacy scope/control runs above do not certify an HTTP allow_for answer or an expired allowance")
	})
}
