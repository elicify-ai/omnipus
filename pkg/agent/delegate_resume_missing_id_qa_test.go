package agent

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// D7 (dispatch diagnostic acceptance): a resume typo must be visible to the
// caller. The release UAT used a helper id with one character omitted, and the
// generic "lifecycle record not found" hid which id had actually been queried.
// No whole-message punctuation is mandated; the exact requested token is.
func TestQADelegateResume_MissingLifecycleNamesRequestedSessionID(t *testing.T) {
	f := newQAUATDelegateFixture(t, "")
	correctID := f.child.SessionID
	requestedID := correctID[:len(correctID)-2] + correctID[len(correctID)-1:]
	before := rootReopenedRecord(t, f.al, correctID)
	if _, err := f.al.GetSessionLifecycleStore().Load(requestedID); !errors.Is(err, session.ErrLifecycleNotFound) {
		t.Fatalf("SETUP: single-character typo %q unexpectedly resolves: %v; correct real helper is %q", requestedID, err, correctID)
	}
	ctx := tools.WithTranscriptSessionID(context.Background(), f.parentID)
	ctx = tools.WithAgentID(ctx, testDefaultAgentID)
	result := delegateToolFor(t, f.al).Execute(ctx, map[string]any{
		"action": "resume", "session_id": requestedID, "text": "Continue the helper's existing conversation.",
	})
	if result == nil || !result.IsError {
		t.Fatalf("resume of missing lifecycle %q returned %+v, want a caller-visible tool error", requestedID, result)
	}
	if !strings.Contains(result.ForLLM, "lifecycle record not found") {
		t.Errorf("resume missing-record error = %q, want the specific lifecycle-record-not-found cause", result.ForLLM)
	}
	// Exact tokens, not substrings: naming the correct longer id must NOT
	// accidentally satisfy the assertion for a shortened/mistyped requested id.
	idsInError := regexp.MustCompile(`\bsession_[[:alnum:]]+\b`).FindAllString(result.ForLLM, -1)
	if !slices.Contains(idsInError, requestedID) {
		t.Errorf("D7: caller-visible resume error %q omits requested session id %q; want the exact looked-up id so the single-character typo against real helper %q is visible", result.ForLLM, requestedID, correctID)
	}
	after := rootReopenedRecord(t, f.al, correctID)
	if !reflect.DeepEqual(after, before) {
		t.Errorf("resume typo changed the existing correct helper: before=%+v after=%+v, want no mutation", before, after)
	}
	if calls := len(f.provider.Requests()); calls != 1 {
		t.Errorf("resume typo started extra provider work: calls=%d, want only the original blocked helper call", calls)
	}
}
