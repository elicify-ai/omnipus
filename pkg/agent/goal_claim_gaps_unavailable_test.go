// #1431 CHECK F1. Keep the legacy retention test's name, but make its
// instrument reach the provider and observe the unavailable outcome.
package agent

import (
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func assertGap1431JudgeUnavailableDoesNotConsumeRound(t *testing.T) {
	t.Helper()
	g := newGap1431Chat(t)
	const answer = "The report is ready; please retain this answer even if checking is unavailable."
	const privateError = "gap1431-private-provider-response"
	judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		// An actual provider refusal, not an expired context before dispatch.
		// 401 is operator-fixable, so the existing classification stops retry
		// immediately; there is no wall-clock wait or retry seam to bypass.
		return nil, &common.ProviderError{Status: 401, Body: privateError, BodyPreview: privateError}
	}}
	g.judge.Provider = judge
	dispatches := recordGoalDispatches(g.al)
	events, stop := recordAgentEvents(t, g.al)
	g.al.dispatchDeferredGoalAdjudication(g.emptyClaim(t, "gap1431-unavailable", answer))
	stop()

	if n := judge.callCount(); n != 1 {
		t.Fatalf("Judge provider calls=%d, want exactly one actual unavailable dispatch; claimless non-dispatch is not retention evidence", n)
	}
	after := mustGoalRecord(t, g.gid)
	if after.State != generated.GoalStateActive || after.Round != 0 {
		t.Errorf("unavailable Judge left durable state/round=%q/%d, want active/0 (ADR-084 section 13 no-round exit)", after.State, after.Round)
	}
	if after.LatestVerdict != nil {
		t.Errorf("unavailable Judge fabricated a verdict: %+v", after.LatestVerdict)
	}
	if after.LatestClaim == nil || after.LatestClaim.Evidence != "" || after.LatestClaim.Status != generated.GoalLatestClaimStatusMet {
		t.Errorf("recorded empty claim lost: LatestClaim=%+v", after.LatestClaim)
	}
	if !strings.Contains(after.LatestReason, "Not verified") || !strings.Contains(after.LatestReason, answer) {
		t.Errorf("unavailable conclusion=%q, want Not verified with the worker answer", after.LatestReason)
	}
	if strings.Contains(after.LatestReason, privateError) {
		t.Errorf("unavailable conclusion leaked the private provider error: %q", after.LatestReason)
	}
	if got := dispatches.all(); len(got) != 0 {
		t.Errorf("unavailable Judge dispatched %d worker steers, want zero", len(got))
	}
	if n := countGoalOutcomeEntries(t, g.store, g.sid); n != 0 {
		t.Errorf("unavailable Judge published %d terminal goal outcomes, want zero", n)
	}
	entries, err := g.store.ReadTranscript(g.sid)
	if err != nil {
		t.Fatalf("read unavailable transcript: %v", err)
	}
	conclusions := 0
	for _, entry := range entries {
		if entry.Type == session.EntryTypeJudgeVerdict {
			t.Errorf("unavailable Judge recorded a fabricated verdict entry: %+v", entry)
		}
		if entry.Type == session.EntryTypeSystem && strings.Contains(entry.Content, "Not verified") && strings.Contains(entry.Content, answer) {
			conclusions++
		}
	}
	if conclusions != 1 {
		t.Errorf("durable result-bearing Not verified conclusions=%d, want exactly one", conclusions)
	}
	frames := goalStatusPayloadsFor(events, g.sid)
	if len(frames) < 2 || frames[0].State != goalPillJudging || frames[len(frames)-1].State != goalPillJudgeUnavailable {
		t.Errorf("unavailable goal frames=%+v, want actual judging then judge_unavailable", frames)
	}
}
