package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R4-RED-1 / D1: rejection belongs at the real, once-resolved Launch boundary,
// before lifecycle/goal/archive writes or any runtime admission/process work.
func TestOpenCodeR4_01_ExternalGoalLaunchRefusesBeforeWrites(t *testing.T) {
	for _, cli := range opencodeR4CLIs {
		t.Run(cli.key, func(t *testing.T) {
			al, provider := opencodeR4Loop(t, cli.key)
			_, processes := opencodeR4ProcessEdge(t, al)
			parent := newSteeringSessionWithActiveAgent(t, al)
			req := opencodeR4Request(parent, delegateExtCLIAgentID, opencodeR4Goal())
			before := opencodeR4Snapshot(t, al)
			res, err := NewSteerLauncher(al).Launch(context.Background(), req)
			assert.Equal(t, steer.LaunchResult{}, res, "D1: a refused Launch has no child identity")
			assert.Equal(t, before, opencodeR4Snapshot(t, al), "D1: zero new lifecycle/goal/session/transcript files or admissions")
			assert.Zero(t, processes.Load(), "D1: Launch must not reach an external driver")
			assert.Zero(t, provider.calls.Load(), "D1: no native fallback may run")
			require.ErrorIs(t, err, tools.ErrDelegateGoalUnsupported, "D1: Launch uses the exported tools sentinel, not a same-text duplicate")
			require.ErrorIs(t, errors.Unwrap(err), tools.ErrDelegateGoalUnsupported, "D1: Launch must wrap the sentinel")
			assert.NotErrorIs(t, err, tools.ErrDelegateTargetCannotReport, "goal refusal has its own sentinel")
			req.Origin.CallID = "r4-second-independent-launch"
			_, again := NewSteerLauncher(al).Launch(context.Background(), req)
			assert.ErrorIs(t, again, tools.ErrDelegateGoalUnsupported, "D1: independently invoked refusals wrap the SAME exported sentinel identity")
			assert.Equal(t, before, opencodeR4Snapshot(t, al), "repeated refusal must also persist nothing")
		})
	}
}

// R4-RED-2 is a positive control, not required to be red on the baseline.
func TestOpenCodeR4_02_ExternalGoalFreeLaunchStillSucceeds(t *testing.T) {
	for _, cli := range opencodeR4CLIs {
		t.Run(cli.key, func(t *testing.T) {
			al, _ := opencodeR4Loop(t, cli.key)
			parent := newSteeringSessionWithActiveAgent(t, al)
			before := opencodeR4Snapshot(t, al)
			res, err := NewSteerLauncher(al).Launch(context.Background(), opencodeR4Request(parent, delegateExtCLIAgentID, nil))
			require.NoError(t, err, "D1 control: nil Goal must remain launchable")
			rec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
			require.NoError(t, err)
			assert.True(t, rec.Is3P)
			assert.Equal(t, delegateExtCLIAgentID, rec.AgentID)
			assert.Equal(t, parent, rec.SteeredBy.SteeringSessionID)
			assert.Empty(t, rec.GoalRef)
			after := opencodeR4Snapshot(t, al)
			// Launch also materializes an absent parent's ordinary_root record.
			// Count the specified child independently, not that unrelated root.
			children, listErr := al.GetSessionLifecycleStore().List(session.LifecycleFilter{SteeringSessionID: parent})
			require.NoError(t, listErr)
			if assert.Len(t, children, 1, "instrument: lifecycle listing detects the accepted child") {
				assert.Equal(t, res.SessionID, children[0].SessionID)
			}
			assert.Equal(t, before.sessions+1, after.sessions)
			assert.Equal(t, before.goals, after.goals)
			assert.Greater(t, len(after.transcripts), len(before.transcripts), "instrument: the traversal sees the accepted child's real transcript")
			assert.Zero(t, after.active)
			assert.Zero(t, after.queued)
		})
	}
}

// R4-RED-3 / D1: the identical goal remains valid for a native delegate.
func TestOpenCodeR4_03_NativeGoalLaunchPersistsTheGoal(t *testing.T) {
	al, _ := opencodeR4Loop(t, "opencode")
	parent := newSteeringSessionWithActiveAgent(t, al)
	before := opencodeR4Snapshot(t, al)
	res, err := NewSteerLauncher(al).Launch(context.Background(), opencodeR4Request(parent, delegateNativeWorkerAgentID, opencodeR4Goal()))
	require.NoError(t, err, "D1 control: native criteria plus DoD must still launch")
	rec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	require.NoError(t, err)
	assert.False(t, rec.Is3P)
	require.NotEmpty(t, rec.GoalRef)
	g, err := resolveGoalRecordStore().Get(rec.GoalRef)
	require.NoError(t, err)
	assert.Equal(t, generated.GoalStateActive, g.State)
	assert.Equal(t, generated.GoalOwnerKindSession, g.OwnerKind)
	assert.Equal(t, res.SessionID, g.OwnerID)
	assert.Equal(t, res.SessionID, g.ActiveSessionID)
	if assert.Len(t, g.Criteria, 1) {
		assert.Equal(t, "The answer contains three numbered steps", g.Criteria[0].Text)
	}
	if assert.Len(t, g.DoD, 1) {
		assert.Equal(t, "The answer is the requested text itself", g.DoD[0].Text)
	}
	assert.NoError(t, g.Validate())
	after := opencodeR4Snapshot(t, al)
	assert.Equal(t, before.goals+1, after.goals, "instrument: the goal list detects a real native goal")
	children, listErr := al.GetSessionLifecycleStore().List(session.LifecycleFilter{SteeringSessionID: parent})
	require.NoError(t, listErr)
	if assert.Len(t, children, 1, "native goal Launch creates exactly this direct child") {
		assert.Equal(t, res.SessionID, children[0].SessionID)
	}
}

// Observes interface calls, forwarding every call to the REAL launcher. This
// is not a fake Launch/Dispatch response: it exposes the tool's no-Dispatch
// promise, while R4-RED-1 separately drives Launch directly.
type opencodeR4LauncherObservation struct {
	real       steer.SessionLauncher
	launches   int
	dispatches int
}

func (o *opencodeR4LauncherObservation) Launch(ctx context.Context, req steer.LaunchRequest) (steer.LaunchResult, error) {
	o.launches++
	return o.real.Launch(ctx, req)
}

func (o *opencodeR4LauncherObservation) Dispatch(ctx context.Context, sid string, gen int) (steer.DispatchResult, error) {
	o.dispatches++
	return o.real.Dispatch(ctx, sid, gen)
}

func opencodeR4GoalArgs() map[string]any {
	return map[string]any{
		"action": "run", "agent_id": delegateExtCLIAgentID, "task": "Write three numbered steps",
		"criteria": []any{map[string]any{"kind": "prose", "judgment": "boolean", "text": "The answer contains three numbered steps"}},
		"dod":      []any{map[string]any{"kind": "prose", "judgment": "boolean", "text": "The answer is the requested text itself"}},
	}
}

func TestOpenCodeR4_04_RealDelegateRefusesExternalGoalWithoutSession(t *testing.T) {
	for _, cli := range opencodeR4CLIs {
		t.Run(cli.key, func(t *testing.T) {
			al, provider := opencodeR4Loop(t, cli.key)
			d, processes := opencodeR4ProcessEdge(t, al)
			parent := newSteeringSessionWithActiveAgent(t, al)
			before := opencodeR4Snapshot(t, al)
			// Independently obtain the Launch cause, not an invented test error.
			_, launchErr := NewSteerLauncher(al).Launch(context.Background(), opencodeR4Request(parent, delegateExtCLIAgentID, opencodeR4Goal()))
			observed := &opencodeR4LauncherObservation{real: NewSteerLauncher(al)}
			tool := delegateToolFor(t, al)
			tool.SetSessionLauncher(observed)
			// Approved TEXT.md section 2: this exact sentence is additive,
			// occurs once, and is not rewritten from runtime output.
			const descriptionLine = "Goals (criteria and dod) are refused for a worker on an external CLI."
			assert.Equal(t, 1, strings.Count(tool.Description(), descriptionLine), "D1: exact approved Description addition occurs once")
			ctx := tools.WithAgentID(tools.WithTranscriptSessionID(context.Background(), parent), testDefaultAgentID)
			ctx = tools.WithToolCallID(ctx, "r4-real-delegate-goal")
			result := tool.Execute(ctx, opencodeR4GoalArgs())
			// Settle a baseline's incorrectly accepted child before reading stores.
			d.finishFirst()
			opencodeFixJoin(t, al)
			require.NotNil(t, result)
			assert.True(t, result.IsError, "D1: goal-bearing external delegate is an error result")
			// Approved TEXT.md section 1 supersedes the temporary keyword
			// check: the code travels separately, not inside this exact prose.
			wantRefusal := fmt.Sprintf("delegate: refused — agent %q cannot take a delegated goal because it runs on an external command-line tool. Delegate again without criteria and dod and check the answer yourself, or use create_task for a board task with a checked goal.", delegateExtCLIAgentID)
			assert.Equal(t, wantRefusal, result.ContentForLLM(), "D1: exact approved refusal, including the quoted target id")
			assert.NotContains(t, result.ContentForLLM(), "session_id", "D1: do not manufacture a child identity")
			assert.Equal(t, 1, observed.launches, "instrument: actual Execute must reach Launch, not fail an earlier gate")
			assert.Zero(t, observed.dispatches, "D1: actual delegate must never call Dispatch after the refusal")
			assert.Zero(t, processes.Load())
			assert.Zero(t, provider.calls.Load())
			assert.Equal(t, before, opencodeR4Snapshot(t, al), "D1: neither Launch nor real delegate may write a refused helper")
			require.ErrorIs(t, launchErr, tools.ErrDelegateGoalUnsupported, "D1: the independent Launch uses the exported tools sentinel")
			require.ErrorIs(t, errors.Unwrap(launchErr), tools.ErrDelegateGoalUnsupported, "D1: Launch wraps the exported sentinel")
			assert.ErrorIs(t, result.Err, tools.ErrDelegateGoalUnsupported, "real tool result must preserve the exported Launch sentinel via errors.Is")
		})
	}
}

// R4-RED-11: goal-free external normal completion plus the existing native
// goal claim/judge/handback regression. The native subtest exercises the real
// finish/claim/judge path with a scripted turn result, not a full model turn.
func TestOpenCodeR4_11_GoalFreeExternalAndNativeGoalRegressions(t *testing.T) {
	for _, cli := range opencodeR4CLIs {
		t.Run(cli.key, func(t *testing.T) {
			al, d, processes, child := opencodeR4Running(t, cli.key)
			d.finishFirst()
			opencodeFixJoin(t, al)
			rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
			require.NoError(t, err)
			assert.Equal(t, session.LifecycleCompleted, rec.State)
			assert.Empty(t, rec.GoalRef)
			assert.EqualValues(t, 1, processes.Load())
			entries, err := al.GetMessageInboxStore().Entries(rec.SteeredBy.SteeringSessionID)
			require.NoError(t, err)
			var answers []string
			for _, entry := range entries {
				if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
					continue
				}
				class, classErr := session.ClassifySessionMessage(*entry.Message)
				require.NoError(t, classErr)
				require.Equal(t, "handback", class.Kind, "goal-free success must not become a parent error")
				handback, decodeErr := entry.Message.AsSessionMessageHandback()
				require.NoError(t, decodeErr)
				assert.Equal(t, child.SessionID, handback.SessionId)
				answers = append(answers, handback.ResultSoFar)
			}
			assert.Equal(t, []string{"original work completed"}, answers, "D4: exactly the real external answer reaches the parent")
		})
	}
	t.Run("native_goal_met_claim_judge_and_handback", TestGoalChildCompletion947_MetArmCompletesChildAndDeliversHandback)
}
