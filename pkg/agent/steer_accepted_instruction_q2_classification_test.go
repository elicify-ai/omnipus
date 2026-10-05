package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// Oracle: ADR-091 D2 and landing-order I-8, plus team-lead's 2026-10-05
// ruling on the accepted-drain timing: retained parent metadata + a vanished
// record must not silently turn a genuine child into non-steered success.
// Return a visible error, restore the suffix, write no consumed marker and
// no transcript line claiming this instruction was injected. No exact UI
// phrase or error type was specified by that ruling; do not invent one.
func TestAcceptedSteeredInstructionQ2_MissingChildLifecycleRefusesAndRestoresSuffix(t *testing.T) {
	q2ConsumerLifecycleFault(t, false)
}

// Oracle: ADR-091 D2/I-8's unreadable row: refuse, report the real read
// failure; accepted input must remain retryable, not turn into ordinary-root
// success. An all-corrupt journal is a real filesystem fault, not a fake
// classifier or a mocked store.
func TestAcceptedSteeredInstructionQ2_UnreadableChildLifecycleRefusesAndRestoresSuffix(t *testing.T) {
	q2ConsumerLifecycleFault(t, true)
}

func q2ConsumerLifecycleFault(t *testing.T, corrupt bool) {
	t.Helper()
	fixture := newQ2ConsumerFixture(t)
	prefix := q2ConsumerEnqueuePlain(t, fixture.al, fixture.childID, "q2-classification-prefix", "Consumed prefix stays consumed.")
	const messageID = "q2-accepted-before-lifecycle-loss"
	const text = "Do not silently consume me without the genuine child's lifecycle."
	failed := q2ConsumerEnqueueWake(t, fixture.al, fixture.childID, messageID, text)
	laterWake := q2ConsumerEnqueueWake(t, fixture.al, fixture.childID, "q2-classification-later-wake", "Keep the subsequent wake's identity.")
	tail := q2ConsumerEnqueuePlain(t, fixture.al, fixture.childID, "q2-classification-tail", "Keep the subsequent correlation id.")
	batch := q2ConsumerDequeue(t, fixture.al, fixture.childID, []steeringQueueItem{prefix, failed, laterWake, tail})

	journal := filepath.Join(fixture.al.GetConfig().Agents.Defaults.Home, "session_lifecycle", fixture.childID+".jsonl")
	original, err := os.ReadFile(journal)
	if err != nil || len(original) == 0 {
		t.Fatalf("SETUP real launched-child journal bytes=%d err=%v", len(original), err)
	}
	t.Cleanup(func() {
		if werr := os.WriteFile(journal, original, 0o600); werr != nil {
			t.Errorf("restore original launched-child journal: %v", werr)
		}
	})
	if corrupt {
		if cerr := os.WriteFile(journal, []byte("{not-a-lifecycle-record}\n"), 0o600); cerr != nil {
			t.Fatalf("install all-corrupt physical journal: %v", cerr)
		}
	} else if rerr := os.Remove(journal); rerr != nil {
		t.Fatalf("remove accepted genuine child's journal: %v", rerr)
	}
	meta, err := fixture.store.GetMeta(fixture.childID)
	if err != nil || meta.ParentSessionID != fixture.parentID {
		t.Fatalf("fault instrument lost genuine parent metadata: meta=%+v err=%v", meta, err)
	}
	_, loadErr := fixture.al.GetSessionLifecycleStore().Load(fixture.childID)
	classifier := NewSteerRecordClassifier(fixture.al.GetSessionLifecycleStore(), fixture.store)
	class, classifyErr := classifier.Classify(context.Background(), fixture.childID)
	if corrupt {
		if loadErr == nil || errors.Is(loadErr, session.ErrLifecycleNotFound) || class != steer.ClassUnreadable || classifyErr == nil {
			t.Fatalf("instrument must see existing unreadable data: load=%v class=%q classify=%v", loadErr, class, classifyErr)
		}
	} else if !errors.Is(loadErr, session.ErrLifecycleNotFound) || class != steer.ClassDamagedChild || classifyErr != nil {
		t.Fatalf("instrument must distinguish lost child from root: load=%v class=%q classify=%v", loadErr, class, classifyErr)
	}

	// Arrival during the failed batch must remain AFTER the restored suffix.
	// This is a plain correlated item; enqueuing another wake through the
	// public lifecycle check would itself fail before testing consumption.
	arrival := steeringQueueItem{message: tail.message, correlationID: "q2-after-dequeue-arrival"}
	if err := fixture.al.steering.pushItemScope(fixture.childID, arrival); err != nil {
		t.Fatalf("SETUP real queue arrival while consumer is in flight: %v", err)
	}
	result := q2ConsumerConsume(fixture.al, fixture.childID, batch)
	if result.err == nil {
		t.Errorf("accepted genuine child with class %q was silently consumed: returned=%d claimed=%d; want visible lifecycle refusal", class, len(result.messages), len(result.items))
	} else {
		if !strings.Contains(result.err.Error(), messageID) || !strings.Contains(result.err.Error(), fixture.childID) {
			t.Errorf("visible refusal does not name failed instruction/session: %v", result.err)
		}
		if corrupt && !strings.Contains(result.err.Error(), loadErr.Error()) {
			t.Errorf("visible refusal dropped real corruption cause: result=%v cause=%v", result.err, loadErr)
		}
	}
	q2ConsumerRequireReturned(t, result, []steeringQueueItem{prefix})
	q2ConsumerRequireQueued(t, fixture.al, fixture.childID, []steeringQueueItem{failed, laterWake, tail, arrival})
	q2ConsumerRequireTail(t, fixture, nil)
}
