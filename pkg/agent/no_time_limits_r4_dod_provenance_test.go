package agent

// PR #1311 ROUND 4 G11: delegate accepts the set_goal DoD item shape,
// including the additive provenance vocabulary. The PUBLIC registry validates
// the actual delegate schema before real Launch/Dispatch; parseDelegateGoal
// called directly would miss the observed 'unexpected property' defect.
// CHECK: remove provenance from the public schema; reject one valid layer;
// remove DoD validation and accept a genuinely invalid item.

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func r4DelegateDoDArgs(provenance string, invalid bool) map[string]any {
	item := map[string]any{"text": "The result is supported by checked evidence", "judgment": "boolean"}
	if provenance != "" {
		item["provenance"] = provenance
	}
	if invalid {
		item["text"] = "   " // real runtime invalidity, not an unrecognised optional property
	}
	return map[string]any{"action": "run", "agent_id": "native-agent", "task": "Produce the checked analysis",
		"criteria": []any{map[string]any{"text": "The requested analysis is complete", "judgment": "boolean"}},
		"dod":      []any{item}}
}

func r4DoDRegistryAcceptsRealLaunch(t *testing.T, provenance string) {
	t.Helper()
	provider := newGatedFinalProvider()
	h := newR4DelegateHarness(t, provider)
	release := sync.OnceFunc(func() { close(provider.release) })
	t.Cleanup(func() { release(); h.al.steerAdmission().turns.Wait() })
	result := h.publicDelegate(r4DelegateDoDArgs(provenance, false))
	if result.IsError {
		t.Fatalf("G11 delegate registry rejected valid set_goal-shaped DoD provenance=%q: %s (must accept provenance, not unexpected property)", provenance, result.ContentForLLM())
	}
	var launched generated.DelegateSessionResponse
	if err := json.Unmarshal([]byte(result.ContentForLLM()), &launched); err != nil {
		t.Fatalf("G11 accepted run did not return the real generated launch response: %v; result=%q", err, result.ContentForLLM())
	}
	if launched.SessionId == "" || launched.Generation != 1 || launched.State != generated.DelegateSessionResponseState(steer.DispatchRunning) {
		t.Fatalf("G11 valid DoD did not reach a real running admission: %+v", launched)
	}
	waitClosed(t, provider.entered, "the accepted provenance child's real provider")
	rec := mustLoadRec(t, h.al, launched.SessionId)
	goal := mustGoalRecord(t, rec.GoalRef)
	if rec.State != session.LifecycleRunning || len(goal.DoD) != 1 || goal.DoD[0].Text != "The result is supported by checked evidence" {
		t.Errorf("G11 accepted DoD was not actually carried into the real helper goal: record=%+v goal=%+v", rec, goal)
	}
}

func TestR4DelegateDoD_ProvenanceShapeIsAcceptedAndInvalidItemIsRefused(t *testing.T) {
	// set_goal.Parameters and ADR-080 D-DOD define all four allowed layers.
	// These are independent declared inputs, not values copied from delegate's
	// currently narrower implementation schema.
	for _, provenance := range []string{"stated", "workspace", "floor", "inferred"} {
		t.Run("valid_"+provenance, func(t *testing.T) { r4DoDRegistryAcceptsRealLaunch(t, provenance) })
	}
	t.Run("valid_legacy_item_without_provenance", func(t *testing.T) { r4DoDRegistryAcceptsRealLaunch(t, "") })
	t.Run("genuinely_invalid_blank_DoD_is_refused", func(t *testing.T) {
		h := newR4DelegateHarness(t, &claimingWorkerProvider{content: "unreachable invalid DoD"})
		result := h.publicDelegate(r4DelegateDoDArgs("", true))
		if !result.IsError || !strings.Contains(result.ContentForLLM(), "dod[0]") ||
			!strings.Contains(result.ContentForLLM(), "text is required") {
			t.Errorf("G11 invalid DoD must remain an honest specific refusal: error=%v result=%q", result.IsError, result.ContentForLLM())
		}
	})
}
