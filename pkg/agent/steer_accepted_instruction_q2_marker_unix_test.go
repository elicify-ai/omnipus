//go:build linux || darwin

package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// Oracle: dispatch Q2(2), preserved ADR-20260928 D4 Delivery: exact input
// text is durable BEFORE the consumed receipt/queue release; a failed marker
// must remain visibly retryable without duplicating that text.
//
// The history cut is established by a REAL successful recorder call, then
// a physical write-permission failure. This does not claim a crash, nor a
// production hook between writes within one consume call. Unix-only fixture:
// Windows chmod does not implement this denial. Do not silently skip when a
// root/bypassing runner defeats the permission instrument; fail BLOCKED.
func TestAcceptedSteeredInstructionQ2_MarkerFailureRestoresSuffixAndRetriesWithoutDuplicate(t *testing.T) {
	fixture := newQ2ConsumerFixture(t)
	// These correlated fillers isolate the wake-marker path, as in the
	// original Q2 test. B2 requires durable transcript injection for a
	// ledgered delegate steer, so it cannot be consumed under this denial.
	prefix := q2ConsumerEnqueueFiller(t, fixture.al, fixture.childID, "q2-marker-prefix", "The consumed prefix is not retried.")
	const messageID = "q2-marker-fails-after-append"
	const text = "Persist this instruction exactly once before its consumed marker."
	failed := q2ConsumerEnqueueWake(t, fixture.al, fixture.childID, messageID, text)
	const suffixID = "q2-marker-suffix-wake"
	const suffixText = "The suffix wake must retain its original identity."
	suffixWake := q2ConsumerEnqueueWake(t, fixture.al, fixture.childID, suffixID, suffixText)
	tail := q2ConsumerEnqueueFiller(t, fixture.al, fixture.childID, "q2-marker-tail", "The suffix correlation must be conserved.")
	batch := q2ConsumerDequeue(t, fixture.al, fixture.childID, []steeringQueueItem{prefix, failed, suffixWake, tail})

	if err := fixture.al.recordAcceptedSteeredInstruction(*failed.wake, failed.message); err != nil {
		t.Fatalf("SETUP actual instruction append before marker failure: %v", err)
	}
	q2ConsumerRequireTail(t, fixture, []session.TranscriptEntry{q2ConsumerInstruction(messageID, text)})
	transcript := filepath.Join(fixture.store.BaseDir(), fixture.childID, "transcript.jsonl")
	stat, err := os.Stat(transcript)
	if err != nil {
		t.Fatalf("SETUP stat actual appended transcript: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(transcript, stat.Mode().Perm()); err != nil {
			t.Errorf("restore transcript write permissions: %v", err)
		}
	})
	if err := os.Chmod(transcript, 0o400); err != nil {
		t.Fatalf("install readable-but-not-writable transcript: %v", err)
	}
	// Test the denial instrument before trusting an error or green result.
	if _, err := os.ReadFile(transcript); err != nil {
		t.Fatalf("instrument denied the instruction read, not just the marker append: %v", err)
	}
	probe, openErr := os.OpenFile(transcript, os.O_RDWR|os.O_APPEND, 0o600)
	if openErr == nil {
		if err := probe.Close(); err != nil {
			t.Errorf("close write-permission instrument probe: %v", err)
		}
		t.Fatal("BLOCKED: runner bypasses transcript write permissions; cannot prove post-append marker failure")
	}
	if !errors.Is(openErr, os.ErrPermission) {
		t.Fatalf("instrument append error = %v, want actual permission denial", openErr)
	}

	result := q2ConsumerConsume(fixture.al, fixture.childID, batch)
	var pathErr *os.PathError
	if !errors.Is(result.err, os.ErrPermission) || !errors.As(result.err, &pathErr) {
		t.Errorf("marker failure returned %v, want visible wrapped permission/path error", result.err)
	} else if pathErr.Path != transcript || pathErr.Op != "open" || !strings.Contains(result.err.Error(), messageID) {
		t.Errorf("marker failure lost exact message/path/open cause: error=%v path=%+v", result.err, pathErr)
	}
	q2ConsumerRequireReturned(t, result, []steeringQueueItem{prefix})
	q2ConsumerRequireTail(t, fixture, []session.TranscriptEntry{q2ConsumerInstruction(messageID, text)})
	if pending := fixture.al.pendingSteeringCountForScope(fixture.childID); pending != 3 {
		t.Errorf("restored suffix count = %d, want 3 (failed wake, later wake, correlated tail)", pending)
	}

	if err := os.Chmod(transcript, stat.Mode().Perm()); err != nil {
		t.Fatalf("repair actual marker-write fault before retry: %v", err)
	}
	arrival := q2ConsumerEnqueueFiller(t, fixture.al, fixture.childID, "q2-marker-arrival", "Later arrival stays behind the restored suffix.")
	retryItems := []steeringQueueItem{failed, suffixWake, tail, arrival}
	retryBatch := q2ConsumerDequeue(t, fixture.al, fixture.childID, retryItems)
	retry := q2ConsumerConsume(fixture.al, fixture.childID, retryBatch)
	if retry.err != nil {
		t.Fatalf("retry after real write repair: %v", retry.err)
	}
	q2ConsumerRequireReturned(t, retry, retryItems)
	q2ConsumerRequireTail(t, fixture, []session.TranscriptEntry{
		q2ConsumerInstruction(messageID, text), q2ConsumerMarker(messageID),
		q2ConsumerInstruction(suffixID, suffixText), q2ConsumerMarker(suffixID),
	})
	if pending := fixture.al.pendingSteeringCountForScope(fixture.childID); pending != 0 {
		t.Errorf("pending queue after repaired retry = %d, want 0", pending)
	}
}
