//go:build goolm && stdjson

package gateway

// Order 52 / CHECK F17: C-26, S-109 and S-113. Moved from pkg/agent so
// the test can drive the real policy approver/registry/HTTP handler without
// a circular production import. No test inserts a pending card or grant.
// Actual task run (TaskExecutor.ExecuteTask), actual Skill, protected generation
// grants, real later model requests/transcript and raw persisted policies are
// observed. Only a scripted no-paid-call provider and a clock are injectable.
//
// The local weather fixture is genuinely granted under trust Allow. It is
// NOT a fabricated hash-verified install. Exact S-109 verification metadata
// and S-113's DIFFERENT unverified/trust-Ask/restart scenario remain explicit
// BLOCKED obligations. A task run is not proof that the scheduler fired or
// that the full gateway restarted.
// Mutation probes and complete GREEN acceptance belong to independent CHECK.
//
// Fix round 4 (team-lead ruling, part B row 8 option 1, part D item 9):
//   - The unattended turn is a real task run, not a cron-job ProcessScheduled turn: S-109
//     says a scheduled TASK, and the "Not run: needs approval, and task and plan runs never
//     ask." sentence below is what a task run is given (C-12's unattended column, ST-Q9 A);
//     a cron job keeps the headless sentence today (#1263 R4,
//     pkg/agent/turn_attendance.go::applyTurnAttendance). Same oracles as before: no card,
//     the model sees exactly one tool result, refused bytes never reach any request, the
//     exact sentence is persisted. The run's task is the runner; nothing here decides
//     whether the Skill may load or supplies the refusal text.
//   - The interactive chat that raises the card carries a lifecycle record. A web chat gets
//     its root record from ordinary admission (pkg/agent/ordinary_root_record.go::
//     ensureOrdinaryRootRecord) before attendance is classified; this fixture enters through
//     ProcessDirectWithChannelAs, which does not pass that admission, so the record is
//     persisted the way skill_consent_table_red_test.go::beginNativeD10 does for its chat
//     rows. Without it #1263 R1 (turn_attendance.go::classifySessionChain) reads the session
//     as "lifecycle record unreadable or missing", classes it unattended and refuses the
//     call before any card exists.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/tests/helpers/agentrecord"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const scheduledWeatherInstructions = "S109-WEATHER-INSTRUCTIONS-9Z"
const scheduledWeatherRefusal = "Not run: needs approval, and task and plan runs never ask."

// Input-only clock boundary. Never returns a card decision or allowance.
var injectScheduledAllowanceClock func(*agent.AgentLoop, func() time.Time, func() time.Duration)

type scheduledAllowanceClock struct {
	mu      sync.Mutex
	wall    time.Time
	elapsed time.Duration
}

func (c *scheduledAllowanceClock) wallNow() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wall
}

func (c *scheduledAllowanceClock) elapsedNow() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.elapsed
}

func (c *scheduledAllowanceClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(d)
	c.elapsed += d
}

type scheduledAllowanceScript struct {
	armed    atomic.Bool
	calls    atomic.Int64
	mu       sync.Mutex
	requests [][]providers.Message
}

func (p *scheduledAllowanceScript) GetDefaultModel() string { return "scheduled-allowance-script" }
func (p *scheduledAllowanceScript) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	// Freeze the actual request at the external model boundary, not an
	// internally computed permission verdict or a test-owned transcript.
	raw, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	var captured []providers.Message
	if err := json.Unmarshal(raw, &captured); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.requests = append(p.requests, captured)
	p.mu.Unlock()
	if p.armed.CompareAndSwap(true, false) {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: fmt.Sprintf("weather-call-%d", p.calls.Add(1)), Type: "function", Name: "Skill", Arguments: map[string]any{"name": "weather"}}}}, nil
	}
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *scheduledAllowanceScript) arm() {
	p.mu.Lock()
	p.requests = nil
	p.mu.Unlock()
	p.armed.Store(true)
}

func (p *scheduledAllowanceScript) captured() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]providers.Message(nil), p.requests...)
}

type scheduledAllowanceHarness struct {
	al      *agent.AgentLoop
	api     *restAPI
	reg     *approvalRegistryV2
	handler *WSHandler
	script  *scheduledAllowanceScript
	home    string
}

func newScheduledAllowanceHarness(t *testing.T, global, own string) *scheduledAllowanceHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, filepath.Join(home, "config.json"))
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	dir := filepath.Join(home, "skills", "weather")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: weather\ndescription: Scheduled weather fixture.\n---\n\n"+scheduledWeatherInstructions+"\n"), 0o600))
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scheduled-allowance-script"}
	cfg.Agents.Defaults.MaxToolIterations = 3
	cfg.Agents.List = nil
	cfg.Sandbox.AutoApprove, cfg.Sandbox.GodMode = false, false
	cfg.Sandbox.SkillTrust = config.SkillTrustLevel("allow")
	cfg.Sandbox.ToolPolicies["Skill"] = "allow"
	cfg.Sandbox.AuditLog = true
	cfg.Tools.Manifest.Compressed = false // Discovery is not the permission boundary.
	cfg.Planning.GoalMaxRounds = 1        // a task run ends after its one scripted tool call (runRealTask).
	require.NoError(t, json.Unmarshal([]byte(`{"skill_policies":{"weather":"`+global+`"}}`), &cfg.Sandbox))
	store := agentstore.New(home)
	for _, id := range []string{"mia", "ray"} {
		record := config.AgentConfig{ID: id, Name: id, Skills: []string{"weather"}}
		require.NoError(t, store.Create(id, &record), "real persistence binds the actual installed generation")
		if id == "mia" && own != "" {
			path := filepath.Join(home, "entities", "agents", id+".json")
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			var literal map[string]any
			require.NoError(t, json.Unmarshal(raw, &literal))
			// The store record is flat: the agent tool settings are the top-level tools member
			// (tests/helpers/agentrecord); tools_cfg is only the REST request name, so a value
			// planted there is dead data no reader ever sees.
			toolsMember, _ := literal["tools"].(map[string]any)
			if toolsMember == nil {
				toolsMember = map[string]any{}
				literal["tools"] = toolsMember
			}
			toolsMember["skills"] = map[string]any{"policies": map[string]string{"weather": own}}
			raw, err = json.Marshal(literal)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, raw, 0o600))
			require.Equal(t, map[string]any{"weather": own}, agentrecord.Policies(t, home, id, agentrecord.Skills),
				"the Given is planted at the carrier the store keeps, not in a place nothing reads")
		}
		persisted, err := store.Get(id)
		require.NoError(t, err)
		require.NotEmpty(t, persisted.SkillGrants["weather"], "actual generation grant, not a name-only allowlist")
		cfg.Agents.List = append(cfg.Agents.List, *persisted)
	}
	// Store the independent raw Given even while today's config type ignores
	// the new map; runtime retention is separately checked below, never assumed.
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var literal map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &literal))
	var sandbox map[string]any
	require.NoError(t, json.Unmarshal(literal["sandbox"], &sandbox))
	sandbox["skill_policies"] = map[string]string{"weather": global}
	literal["sandbox"], err = json.Marshal(sandbox)
	require.NoError(t, err)
	raw, err = json.Marshal(literal)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), raw, 0o600))
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	script := &scheduledAllowanceScript{}
	al := mustAgentLoop(t, cfg, msgBus, script)
	for _, id := range []string{"mia", "ray"} {
		inst, ok := al.GetRegistry().GetAgent(id)
		require.True(t, ok)
		persisted, err := store.Get(id)
		require.NoError(t, err)
		inst.ContextBuilder.WithSkillAllowlist(persisted.Skills).WithSkillBindings(persisted.SkillGrants)
		_, found := inst.ContextBuilder.ResolveSkillFullForWorkspace("", "weather")
		require.True(t, found, "fixture: real weather instructions resolve for %s", id)
	}
	reg := newApprovalRegistryV2(64, 30*time.Second)
	handler := newWSHandler(msgBus, al, "")
	handler.approvalRegV2 = reg
	al.SetToolApprover(newPolicyApproverAdapter(reg, handler))
	t.Cleanup(handler.Wait)
	return &scheduledAllowanceHarness{al: al, api: &restAPI{agentLoop: al, approvalReg: reg, homePath: home, auditor: al.AuditLogger()}, reg: reg, handler: handler, script: script, home: home}
}

func (h *scheduledAllowanceHarness) assertModelResult(t *testing.T, loads bool) {
	t.Helper()
	requests := h.script.captured()
	require.GreaterOrEqual(t, len(requests), 2, "actual tool result must reach a later model request")
	var results []string
	for _, message := range requests[1] {
		if message.Role == "tool" {
			results = append(results, message.Content)
		}
	}
	require.Len(t, results, 1)
	if loads {
		assert.Contains(t, results[0], scheduledWeatherInstructions, "the actual Skill tool returned instructions")
	} else {
		for _, request := range requests {
			raw, err := json.Marshal(request)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), scheduledWeatherInstructions, "refused skill bytes never reach ANY real model request")
		}
	}
}

// runRealTask runs one real task of owner (prompt as its task prompt) through the loop's own
// TaskExecutor and returns the task run's session id once the run has ended. It never decides
// whether a tool may run, never supplies the refusal text and never writes a transcript: the
// caller reads the real persisted transcript and the real model requests. The mirror of
// pkg/agent/unattended_task_run_helper_test.go::runRealUnattendedTask for this package.
//
// The run ends deterministically after the one scripted tool call: the scripted model answers
// plain text and never claims the goal, so with one goal try (cfg.Planning.GoalMaxRounds = 1,
// set by newScheduledAllowanceHarness) and one task attempt the run spends its only try and
// the task ends Failed. Whether it ends Done or Failed is not what these tests observe.
func (h *scheduledAllowanceHarness) runRealTask(t *testing.T, owner, prompt string) string {
	t.Helper()
	store, executor := agent.GetTaskStore(h.al), agent.GetTaskExecutor(h.al)
	require.NotNil(t, store, "fixture: the loop owns the real task store")
	require.NotNil(t, executor, "fixture: the loop owns the real task executor")
	oneAttempt := 1
	runner := &task.Task{
		Title:       "Unattended run of " + owner,
		Prompt:      prompt,
		Description: "The task whose real run is the unattended turn under test.",
		AgentID:     owner,
		Action:      task.ActionLLM,
		Priority:    3,
		WorkspaceID: testHarnessWorkspaceMembershipID,
		Status:      task.StatusNext,
		MaxAttempts: &oneAttempt,
		// One acceptance criterion: a task goal record with none leaves the goal's first move
		// open, and the engine then narrows the first request to set_goal (and AskUserQuestion)
		// (pkg/agent/goal_loop_forcing.go::goalTurnRecordState, evaluateGoalForcing), so the
		// scripted first call to the tool under test would be refused as "not offered in this
		// request" and never reach the permission decision. The goal record takes its criteria
		// from the stored task (tools.SyncTaskGoalRecord).
		Criteria: []task.AcceptanceCriterion{{
			Kind:   task.KindProse,
			Text:   "The one scripted model turn of this task has run.",
			Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "ta-fixture"},
		}},
	}
	require.NoError(t, store.Create(runner))
	// Pair the task with its goal record the way production task creation pairs it
	// (tools.SyncTaskGoalRecord, both lists given together, GOAL-FR-003/FR-048). The first draft
	// of this fixture skipped this step; the run then executed with "NO goal loop" ("task has no
	// paired goal record", GOAL-FR-023), never reached a terminal status and every caller timed
	// out waiting (proof log fix4-go-fix4d-sched-after.log). That was a defect of the fixture.
	stored, err := store.Get(runner.ID)
	require.NoError(t, err)
	dod := []task.AcceptanceCriterion{{
		Kind:   task.KindProse,
		Text:   "The one scripted model turn of this task has run.",
		Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "ta-fixture"},
	}}
	require.NoError(t, tools.SyncTaskGoalRecord(tools.GoalStoreForTasks(store), stored, true, dod, true,
		func() int { return 1 }))
	// A dispatch whose predecessor on this loop has not yet released its dispatch slot gets
	// agent.ErrDispatchCapReached ("retry later": the executor's documented, retryable refusal),
	// so that one error is retried and nothing else is: any other refusal fails the fixture.
	dispatchDeadline := time.Now().Add(30 * time.Second)
	for {
		dispatchErr := executor.ExecuteTask(context.Background(), runner.ID, nil)
		if dispatchErr == nil {
			break
		}
		require.ErrorIs(t, dispatchErr, agent.ErrDispatchCapReached,
			"ExecuteTask accepts the dispatch synchronously; the run itself is asynchronous")
		require.True(t, time.Now().Before(dispatchDeadline),
			"fixture: the previous run never released its dispatch slot: %v", dispatchErr)
		time.Sleep(20 * time.Millisecond)
	}

	// Wait for the task to be terminal. This helper must not drain the executor afterwards:
	// TaskExecutor.Drain is a one-way shutdown gate, every later dispatch on the same loop is
	// then refused ("task_executor: executor is draining for shutdown — new dispatch refused"),
	// and the rows of this file run several tasks on one loop (a run before the answer, runs
	// after it, runs after the clock passes the end of the allowance). The first draft drained
	// after each run and the second run of every such row failed before anything was observed
	// (proof log fix4-go-fix4e-gateway-sched.log, run against dev code at 186e99344, not a
	// product PASS): a defect of this fixture. Nothing observed here needs the drain: the denial
	// text is persisted by the turn, which ends before the task's terminal status is written, and
	// the loop's own Close drains the executor at the end of the test. The first draft also
	// required the run's session to read as archived (as pkg/agent's t3WaitForTerminal does); in
	// an earlier proof run (fix4-go-fix4d-agent-unattended.log, run against dev code at
	// 901c7381f) two agent-package rows with identical data differed on that flag (0.95 s against
	// a 25 s timeout) although the product log of both shows the same ending, and the archive
	// flag is not what these tests observe. The session status is logged, not asserted.
	deadline := time.Now().Add(30 * time.Second)
	var final *task.Task
	// final is assigned only on the line before the break, so the loop needs no test of its own
	// on it: the deadline is the only way out without a result.
	for time.Now().Before(deadline) {
		got, getErr := store.Get(runner.ID)
		require.NoError(t, getErr)
		if task.IsTerminal(got.Status) {
			final = got
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final == nil {
		got, getErr := store.Get(runner.ID)
		require.NoError(t, getErr)
		t.Fatalf("fixture: the task run did not reach a terminal status within 30 s: status %q, %d attempt(s), result %q",
			got.Status, got.AttemptCount, got.Result)
	}
	require.NotEmpty(t, final.SessionID,
		"fixture: the task run never reached a session, so nothing was observed (task result: %q)", final.Result)
	meta, err := h.al.GetSessionStore().GetMeta(final.SessionID)
	require.NoError(t, err)
	require.Equal(t, runner.ID, meta.TaskID,
		"instrument: the observed session is the real task run's session, the one the unattended classification reads")
	t.Logf("observed: the run ended with task status %q, %d attempt(s), session status %q",
		final.Status, final.AttemptCount, meta.Status)
	return final.SessionID
}

func (h *scheduledAllowanceHarness) scheduled(t *testing.T, owner string, loads bool) {
	t.Helper()
	h.script.arm()
	sessionID := h.runRealTask(t, owner, "Load weather.")
	assert.Empty(t, h.reg.pendingApprovals(), "unattended turns never raise a card")
	h.assertModelResult(t, loads)
	entries, err := h.al.GetSessionStore().ReadTranscript(sessionID)
	require.NoError(t, err)
	var texts []string
	for _, entry := range entries {
		texts = append(texts, entry.Content)
		for _, call := range entry.ToolCalls {
			if text, ok := call.Result["text"].(string); ok {
				texts = append(texts, text)
			}
		}
	}
	if loads {
		assert.NotContains(t, strings.Join(texts, "\n"), scheduledWeatherRefusal)
	} else {
		assert.Contains(t, texts, scheduledWeatherRefusal, "the exact S-109 refusal is actually persisted")
	}
}

func (h *scheduledAllowanceHarness) pendingFromRealChat(t *testing.T) (*approvalEntry, <-chan error) {
	t.Helper()
	h.script.arm()
	meta, err := h.al.GetSessionStore().NewSession(session.SessionTypeChat, "web", "mia")
	require.NoError(t, err)
	// See the header: the chat's root record, exactly as beginNativeD10 persists it for a chat.
	require.NoError(t, h.al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning, AgentID: "mia",
		OwnerScopeKind: session.OwnerScopeHuman, Origin: &session.Origin{Kind: session.OriginKindChat},
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		_, runErr := h.al.ProcessDirectWithChannelAs(ctx, "mia", "Load weather.", "agent:mia:session:"+meta.ID, "webchat", "weather-chat")
		done <- runErr
		close(finished)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("fixture: original interactive turn did not stop")
		}
	})
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if pending := h.reg.pendingApprovals(); len(pending) != 0 {
			require.Len(t, pending, 1)
			require.Equal(t, "mia", pending[0].AgentID)
			require.Equal(t, "Skill", pending[0].ToolName)
			require.Equal(t, map[string]any{"name": "weather"}, pending[0].Args)
			return pending[0], done
		}
		select {
		case runErr := <-done:
			require.NoError(t, runErr, "a real turn error is not the expected permission card")
			h.assertModelResult(t, false)
			t.Fatal("C-26/S-109: the real interactive Skill call completed without the required permission card")
		case <-ctx.Done():
			t.Fatal("UNVERIFIED: the real chat reached neither a card nor completion; deadline is a fixture failure")
		case <-tick.C:
		}
	}
}

func TestTimedAllowance_ReachesScheduledRun(t *testing.T) {
	for _, owner := range []string{"mia", "ray"} {
		t.Run("S-109 before answer "+owner+" really refuses", func(t *testing.T) {
			h := newScheduledAllowanceHarness(t, "ask", "")
			h.scheduled(t, owner, false)
		})
		t.Run("instrument granted Allow really loads for "+owner, func(t *testing.T) {
			h := newScheduledAllowanceHarness(t, "allow", "")
			h.scheduled(t, owner, true)
		})
	}
	for _, own := range []string{"", "ask"} {
		t.Run("S-109 actual chat HTTP answer reaches scheduled calls own="+own, func(t *testing.T) {
			h := newScheduledAllowanceHarness(t, "ask", own)
			entry, done := h.pendingFromRealChat(t)
			snap := h.handler.pendingApprovalSnapshot(entry)
			// C-24 permission-only skill card; unlike an ordinary tool
			// card, this new card's specified actions do not include Cancel.
			assert.Equal(t, []string{"allow_once", "allow_for", "deny"}, snap.OfferedActions)
			assert.Equal(t, []string{"1h", "3h", "1d"}, snap.AllowDurations)
			nativebinding.Require(t, "pkg/agent", injectScheduledAllowanceClock,
				"C-26/S-109: named loop-to-ApprovalGrantStore wall/monotonic input; actual card/handler/store are not replaceable")
			clock := &scheduledAllowanceClock{wall: time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)}
			injectScheduledAllowanceClock(h.al, clock.wallNow, clock.elapsedNow)
			before := h.storedPolicyBytes(t)
			w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"allow_for","duration":"1d"}`)
			require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
			var response gen.ToolApprovalResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.NotNil(t, response.ExpiresAt)
			require.Equal(t, time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC), response.ExpiresAt.UTC())
			require.Equal(t, boolPtr(true), response.GrantRecorded)
			require.NoError(t, <-done, "the original waiting call is released")
			h.assertModelResult(t, true)
			h.scheduled(t, "mia", true)
			h.scheduled(t, "ray", false)
			assert.Equal(t, before, h.storedPolicyBytes(t), "no global/agent policy is persisted by a card answer")
			t.Run("actual audit subject and decision", func(t *testing.T) {
				h.assertAllowanceAudit(t, entry.ApprovalID, response.ExpiresAt.UTC())
			})
			// An absent audit carrier must fail loudly without hiding the
			// independent scheduled-expiry assertions that follow.
			clock.advance(24*time.Hour - time.Nanosecond)
			h.scheduled(t, "mia", true)
			clock.advance(time.Nanosecond)
			h.scheduled(t, "mia", false)
			clock.advance(time.Nanosecond)
			h.scheduled(t, "mia", false)
		})
	}
	t.Run("C-26 runtime retains the literal global and own skill permissions", func(t *testing.T) {
		h := newScheduledAllowanceHarness(t, "ask", "ask")
		raw, err := json.Marshal(h.al.GetConfig().Sandbox)
		require.NoError(t, err)
		var runtime map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &runtime))
		if len(runtime["skill_policies"]) == 0 {
			t.Fatal("BLOCKED: sandbox.skill_policies is not implemented — required by C-2/C-26/S-109; the independent raw disk Given above must not certify an ignored runtime field")
		}
		var policies map[string]string
		require.NoError(t, json.Unmarshal(runtime["skill_policies"], &policies))
		assert.Equal(t, "ask", policies["weather"])
		for _, record := range h.al.GetConfig().Agents.List {
			if record.ID != "mia" {
				continue
			}
			raw, err = json.Marshal(record)
			require.NoError(t, err)
			var entity map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &entity))
			// A marshalled AgentConfig is flat: tools is the agent tool settings member.
			tools := map[string]json.RawMessage{}
			if len(entity["tools"]) != 0 {
				require.NoError(t, json.Unmarshal(entity["tools"], &tools))
			}
			if len(tools["skills"]) == 0 {
				t.Fatal("BLOCKED: agent tools.skills.policies is not implemented — required by C-4/C-26 point 5; a global Ask alone does not prove own-Ask retention")
			}
			var own map[string]map[string]string
			require.NoError(t, json.Unmarshal(tools["skills"], &own))
			assert.Equal(t, "ask", own["policies"]["weather"])
			return
		}
		t.Fatal("fixture: Mia is missing from the actual runtime roster")
	})
	// The two placeholders below were written while C-5 (verified install metadata) and C-24
	// (durable version approval, combined card) did not exist in the product. Fix round 4, part E
	// re-read them against the dev branch (proof target 186e99344): both now exist there
	// (TestSkillConsent_Table/C-5_verified_fixture_uses_native_registry_review_install_and_independent_inventory,
	// TestSkillConsent_Table/D10#30_real_same-home_restart_after_allowance and
	// TestSkillApprovalActions_Handler/C-24_actual_combined_card_answer_releases_real_instructions_and_persists_trust
	// pass there), so "not implemented" would now be a false claim. What is missing is the
	// composite scenario itself in this scheduled-run harness; the messages say so. They stay
	// loud failures, never skips, until the composites are written (open item in fix4-report.md).
	t.Run("S-109 exact verified weather prerequisite", func(t *testing.T) {
		t.Fatal("BLOCKED: test-side gap, not a product gap — the exact S-109 Given (verified `weather`, C-5) is not yet driven in this scheduled-run harness, which still installs weather as a plain folder; the verified install review and publication it needs exist in the dev branch (see TestSkillConsent_Table/C-5_verified_fixture_...). Required by S-109; a trust-Allow runtime probe above is not a verified install")
	})
	t.Run("S-113 actual gateway restart retains R1 trust but ends allowance", func(t *testing.T) {
		t.Fatal("BLOCKED: test-side gap, not a product gap — the S-113 composite (trust Ask, notes-helper R1, Mia/Ray own-Deny, combined-card answer, scheduled runs, real restart, permission-only then R2 combined card) is not yet written; its parts exist in the dev branch (TestSkillConsent_Table/D10#30 restart, TestSkillApprovalActions_Handler combined-card answer). Required by S-113; a new store/loop alone is not gateway restart proof")
	})
}

func (h *scheduledAllowanceHarness) storedPolicyBytes(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, name := range []string{"config.json", "entities/agents/mia.json", "entities/agents/ray.json"} {
		raw, err := os.ReadFile(filepath.Join(h.home, name))
		require.NoError(t, err)
		out[name] = raw
	}
	var cfg map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out["config.json"], &cfg))
	var sandbox map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(cfg["sandbox"], &sandbox))
	var policies map[string]string
	require.NoError(t, json.Unmarshal(sandbox["skill_policies"], &policies))
	require.Equal(t, "ask", policies["weather"], "independent raw global policy remains Ask")
	return out
}

// assertAllowanceAudit holds the one approval-decision entry of the actual card to spec C-26
// (Audit paragraph): "A non-shell timed allowance writes the existing approval-decision entry
// with action: allow_for, duration, expires_at, the agent and the tool or skill key."
//
// Fix round 4, part E (fixture defect found in the fix4e3 run): the first version read the
// entry's content from a member named "details". The audit log's record envelope keeps the
// content of an approval decision under "fields" (pkg/audit Emit; every sibling reader,
// skill_approval_actions_red_test.go::assertNativeSkillAudit and
// skill_consent_table_red_test.go::assertNativeD10TimedAudit, reads it there), so the filter saw
// no entry at all and the test failed on an empty list instead of on the product. It also
// compared expires_at as a formatted string; the entry's time is RFC 3339 with nanoseconds, so
// the end time is now parsed and compared as an instant, as the sibling readers do (a robustness
// change, not the cause of the failure: this fixture's end time has no fractional part).
//
// The spec fixes the content ("the agent and the tool or skill key") but not the member that
// carries the skill key, so the key is asserted as a value of the entry, never through an
// invented member name. The earlier unconditional BLOCKED failure that stood here claimed the
// skill-subject encoding was not implemented; the entry does carry the key, so that claim was
// wrong for the dev code and the assertion above now does the work: an entry without the key
// still fails this test, loudly.
func (h *scheduledAllowanceHarness) assertAllowanceAudit(t *testing.T, approvalID string, end time.Time) {
	t.Helper()
	rows := readAllAuditEntries(t, filepath.Join(h.home, "system"))
	var matching []map[string]any
	for _, row := range rows {
		fields, _ := row["fields"].(map[string]any)
		if row["event"] == audit.EventToolPolicyAskGranted && fields["approval_id"] == approvalID {
			matching = append(matching, fields)
		}
		raw, err := json.Marshal(row)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "sandbox.skill_policies", "S-109: no stored permission-change audit")
	}
	require.Len(t, matching, 1, "one real approval-decision entry for the actual card")
	fields := matching[0]
	assert.Equal(t, "mia", fields["agent_id"])
	assert.Equal(t, "admin", fields["approver_user_id"])
	assert.Equal(t, "allow_for", fields["action"])
	assert.Equal(t, "1d", fields["duration"])
	assert.Contains(t, nativeAuditScalarValues(fields), "weather",
		"C-26 Audit: the entry carries the skill key; no member name is invented for it")
	rawEnd, ok := fields["expires_at"].(string)
	require.True(t, ok, "expires_at is a time string")
	got, err := time.Parse(time.RFC3339Nano, rawEnd)
	require.NoError(t, err)
	assert.Equal(t, end.UTC(), got.UTC(), "the entry states the answer's end time")
}
