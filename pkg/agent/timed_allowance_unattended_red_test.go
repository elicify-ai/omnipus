//go:build goolm && stdjson

package agent

// Order 61 / CHECK F17: S-123, C-26 points 2, 3, 5 and 6, D17#13.
// Plan: run a real task run (TaskExecutor.ExecuteTask, see
// unattended_task_run_helper_test.go) -> real Ask/standing-grant decision -> real
// delete_task -> actual task files, later model request and stored transcript.
// Only the scripted no-paid-call provider and an unexpected-card observer are
// substituted. No callback decides whether execution is allowed or returns
// refusal text. Every task is genuinely assigned to the executing agent.
//
// The legacy Record calls in the rows below expose the real store's agent/session/argument
// boundary defects. They do NOT constitute a timed card answer. The timed answer, the injected
// clock and the one-hour end are exercised by the last subtest (fix round 4, part E: it was a
// loud BLOCKED until the store's duration API and AgentLoop.SetAllowanceClock existed); the
// card route's own HTTP allow_for answer belongs to the gateway tests that name it there. No
// expectation may be weakened or a legacy grant called timed.
//
// Fix round 4 (team-lead ruling, part B row 8 option 1): the unattended turn is a real
// task run, not a cron-job ProcessScheduled turn. S-123 says a scheduled TASK, and the
// "Not run: needs approval, and task and plan runs never ask." sentence is what a task run
// is given (C-12's unattended column, ST-Q9 A); a cron job keeps the headless sentence
// today (#1263 R4, pkg/agent/turn_attendance.go::applyTurnAttendance). Consequences kept
// visible rather than hidden: (1) the delete target is a SEPARATE task owned by the acting
// agent, because the run's own task is the runner; (2) the run's session does not exist
// until the run starts, so a grant can no longer be recorded "in the same session" before
// it; C-26 Key says "The session id is no longer part of the key", so every grant row
// records the grant in another chat and the old same-session / other-chat split is one
// oracle now (the row that carried "same-session" in its name was renamed, nothing else
// about it changed).
// Mutation targets (independent CHECK): skip grant lookup, run without consent,
// share Mia's grant with Jim, fingerprint ordinary arguments, bypass Deny, emit
// an approval for an unattended call, or report deletion without deleting.
// GREEN, mutation and complete S-123 acceptance are deferred to CHECK.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
		name, global, own, actor, grantAgent string
		grant, otherArgs, deleted            bool
	}{
		{name: "S-123 no allowance refuses without a card", global: "ask", actor: "mia"},
		{name: "instrument Allow executes the actual task tool", global: "allow", actor: "mia", deleted: true},
		{name: "instrument real grant releases the actual task tool", global: "ask", actor: "mia", grantAgent: "mia", grant: true, deleted: true},
		{name: "C-26 Mia's other-chat grant reaches a fresh scheduled run", global: "ask", actor: "mia", grantAgent: "mia", grant: true, deleted: true},
		{name: "D17#13 ordinary whole-tool grant covers different task arguments", global: "ask", actor: "mia", grantAgent: "mia", grant: true, otherArgs: true, deleted: true},
		{name: "C-26 Mia's grant never covers Jim", global: "ask", actor: "jim", grantAgent: "mia", grant: true},
		{name: "C-26 global Deny refuses even an existing grant", global: "deny", actor: "mia", grantAgent: "mia", grant: true},
		{name: "C-26 agent's own Ask can be answered from another chat", global: "allow", own: "ask", actor: "mia", grantAgent: "mia", grant: true, deleted: true},
		{name: "C-26 agent's own Deny refuses even an existing grant", global: "allow", own: "deny", actor: "mia", grantAgent: "mia", grant: true},
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
			cfg.Planning.GoalMaxRounds = 1        // the run ends after its one scripted tool call (runRealUnattendedTask).
			args := map[string]any{"task_id": "s123-target-task"}
			encodedArgs, err := json.Marshal(args)
			require.NoError(t, err)
			provider := testutil.NewScenario().WithToolCall("delete_task", string(encodedArgs)).WithText("done")
			msgBus := bus.NewMessageBus()
			t.Cleanup(msgBus.Close)
			al := mustNewAgentLoop(t, cfg, msgBus, provider)
			t.Cleanup(al.Close)
			installTestSessionLauncher(t, al) // the production task-start wiring: session task id + lifecycle record
			actor, ok := al.GetRegistry().GetAgent(row.actor)
			require.True(t, ok)
			_, registered := actor.Tools.Get("delete_task")
			require.True(t, registered, "fixture: actual production deletion tool is registered")
			// StoreToolPolicy replaces the agent's whole policy snapshot, so goal_claim is stated
			// beside the delete_task entry exactly as task_executor_tool_denial_test.go does: the
			// shipped default is allow for every agent, and a task worker denied goal_claim ends
			// before its first turn, which would never reach the decision this test is about.
			own := map[string]config.ToolPolicy{tools.GoalClaimToolName: config.ToolPolicyAllow}
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
			if row.grant {
				// C-26 Key: "The session id is no longer part of the key" (spec line 542), and the
				// run's own session does not exist before the run starts, so the grant is recorded
				// as given in another chat.
				sourceSession := "other-interactive-chat"
				recordedArgs := args
				if row.otherArgs {
					recordedArgs = map[string]any{"task_id": "other-task-argument-A"}
				}
				require.True(t, al.ApprovalGrants().Record(sourceSession, row.grantAgent, "delete_task", recordedArgs), "instrument: real legacy grant recorded; no timed-answer claim")
				require.True(t, al.ApprovalGrants().IsAllowed(sourceSession, row.grantAgent, "delete_task", recordedArgs), "instrument: store lookup sees the record before the actual run")
			}
			sessionID := runRealUnattendedTask(t, al, row.actor, "Delete the assigned target task.")
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
			entries, err := al.GetSessionStore().ReadTranscript(sessionID)
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

	t.Run("S-123 actual timed answer and controlled one-hour expiry", testS123TimedAnswerAndOneHourExpiry)
}

// allowanceTestClock is the injected clock of S-123: the allowance store measures lengths on its
// monotonic reading and reports ends on its wall reading (C-26), and only advance moves either.
type allowanceTestClock struct {
	mu      sync.Mutex
	start   time.Time
	elapsed time.Duration
}

func (c *allowanceTestClock) wallNow() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.start.Add(c.elapsed)
}

func (c *allowanceTestClock) elapsedNow() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.elapsed
}

func (c *allowanceTestClock) advance(d time.Duration) {
	c.mu.Lock()
	c.elapsed += d
	c.mu.Unlock()
}

// readDeleteTaskRefusals returns the persisted denial texts of every refused delete_task call of
// one run's session, and the run's whole persisted transcript text.
func readDeleteTaskRefusals(t *testing.T, al *AgentLoop, sessionID string) (denied []string, transcript string) {
	t.Helper()
	entries, err := al.GetSessionStore().ReadTranscript(sessionID)
	require.NoError(t, err)
	var all strings.Builder
	for _, entry := range entries {
		all.WriteString(entry.Content)
		all.WriteByte('\n')
		for _, call := range entry.ToolCalls {
			if call.Tool == "delete_task" && call.Status == "denied" {
				text, ok := call.Result["text"].(string)
				require.True(t, ok, "actual persisted denial carries display text")
				denied = append(denied, text)
				all.WriteString(text)
			}
		}
	}
	return denied, all.String()
}

// S-123 (spec: "Unattended runs honour a running allowance and never wait after it ends"): the
// Given is delete_task at global Ask, Auto-approve off, a scheduled task of Mia that calls
// delete_task with arguments A, and Mia's card for that call answered with "Allow for 1 hour";
// When the task runs within the hour, and again after the test clock passes the hour; Then the
// first run deletes the task with no card, the second is refused with "Not run: needs approval,
// and task and plan runs never ask.", no card appears and the run does not wait.
//
// The answer is recorded with the store's own timed API (RecordFor, the call a card's allow_for
// answer ends in; the HTTP answer itself is exercised at the gateway by TestTimedAllowance_
// ReachesScheduledRun and the allow_for rows of tool_approvals_timed_actions_red_test.go, whose
// package owns the card route). The clock is the loop's own input, AgentLoop.SetAllowanceClock.
// Both runs are real task runs through the loop's TaskExecutor (unattended_task_run_helper_test.go);
// only the scripted no-paid-call provider and an unexpected-card observer are substituted.
func testS123TimedAnswerAndOneHourExpiry(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "unattended-task-script"}
	cfg.Agents.Defaults.MaxToolIterations = 3
	cfg.Agents.List = []config.AgentConfig{{ID: "mia", Name: "Mia"}, {ID: "jim", Name: "Jim"}}
	cfg.Sandbox.AutoApprove, cfg.Sandbox.GodMode = false, false
	cfg.Sandbox.ToolPolicies["delete_task"] = "ask"
	cfg.Tools.Manifest.Compressed = false // Discovery is not the consent boundary.
	cfg.Planning.GoalMaxRounds = 1        // each run ends after its one scripted tool call (runRealUnattendedTask).
	args := map[string]any{"task_id": "s123-target-task"}
	encodedArgs, err := json.Marshal(args)
	require.NoError(t, err)
	// One scripted tool call and one closing text for each of the two runs.
	provider := testutil.NewScenario().
		WithToolCall("delete_task", string(encodedArgs)).WithText("done").
		WithToolCall("delete_task", string(encodedArgs)).WithText("done")
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)
	installTestSessionLauncher(t, al) // the production task-start wiring: session task id + lifecycle record
	actor, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	// StoreToolPolicy replaces the agent's whole policy snapshot, so goal_claim is stated beside
	// delete_task exactly as the rows above do (a task worker denied goal_claim never reaches the call).
	actor.StoreToolPolicy(&tools.ToolPolicyCfg{
		GlobalPolicies: map[string]config.ToolPolicy{"delete_task": config.ToolPolicyAsk},
		Policies:       map[string]config.ToolPolicy{tools.GoalClaimToolName: config.ToolPolicyAllow},
	})
	approver := &autoRecordingApprover{approve: false}
	al.SetToolApprover(approver)
	store := GetTaskStore(al)
	require.NotNil(t, store)

	clock := &allowanceTestClock{start: time.Date(2026, time.November, 25, 12, 0, 0, 0, time.UTC)}
	al.SetAllowanceClock(clock.wallNow, clock.elapsedNow)

	// Mia's card for delete_task with arguments A, answered with "Allow for 1 hour".
	end, recorded := al.ApprovalGrants().RecordFor("mia", "delete_task", args, time.Hour)
	require.True(t, recorded, "the store records the timed answer of an ordinary tool")
	require.Equal(t, clock.start.Add(time.Hour), end, "instrument: the hour is measured on the injected clock")

	newTarget := func() (*task.Task, string) {
		target := &task.Task{ID: "s123-target-task", Title: "S-123 deletion target", Action: task.ActionLLM, AgentID: "mia", WorkspaceID: testHarnessWorkspaceMembershipID}
		require.NoError(t, store.CreateByAgent(target, "mia"))
		return target, filepath.Join(store.Dir(), target.ID+".json")
	}

	// First run: half an hour into the hour.
	clock.advance(30 * time.Minute)
	target, path := newTarget()
	first := runRealUnattendedTask(t, al, "mia", "Delete the assigned target task.")
	assert.Empty(t, approver.requests(), "S-123: a run inside the hour raises no card")
	_, getErr := store.Get(target.ID)
	assert.ErrorIs(t, getErr, task.ErrNotFound, "S-123: the first run deletes the task")
	_, statErr := os.Stat(path)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "S-123: the persisted task file is gone")
	refused, transcript := readDeleteTaskRefusals(t, al, first)
	assert.Empty(t, refused, "S-123: the covered call is not refused")
	assert.NotContains(t, transcript, unattendedSkillRefusal)
	require.Len(t, provider.AllRequests(), 2, "instrument: the first run consumed exactly its two scripted answers")

	// Second run: the test clock passes the hour (30 + 31 minutes).
	clock.advance(31 * time.Minute)
	require.False(t, al.ApprovalGrants().IsAllowed("other-interactive-chat", "mia", "delete_task", args),
		"instrument: the allowance has ended on the injected clock")
	target, path = newTarget()
	before, err := os.ReadFile(path)
	require.NoError(t, err, "instrument: the second target exists before the second run")
	second := runRealUnattendedTask(t, al, "mia", "Delete the assigned target task.")
	assert.Empty(t, approver.requests(), "S-123: no card appears after the hour, and the run did not wait for one")
	require.NotEqual(t, first, second, "instrument: the second run is its own session")
	refused, _ = readDeleteTaskRefusals(t, al, second)
	assert.Equal(t, []string{"Not run: needs approval, and task and plan runs never ask."}, refused,
		"S-123: after the hour the call is refused with the exact sentence")
	_, getErr = store.Get(target.ID)
	require.NoError(t, getErr, "a refused task is never deleted")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a refused call never rewrites the task")
}
