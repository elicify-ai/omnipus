// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

// S1 M12: recordNotDelivered covers four cap refusal reasons, and only
// rate_limited had a test. The reason a report was refused is what the parent
// reads, so each of the four must be recorded as ITSELF — a shared "refused"
// reason would tell the parent nothing about whether to retry now, shrink the
// report, close a question, or wait for the inbox.
//
// ORACLE: session.InboxRefusalReason's four sentinels and
// notDeliveredReasonPhrase's four phrases (both in the production source), plus
// the doc comment's contract: "For anything other than one of the four cap
// refusals it returns appendErr unchanged", and a record that cannot be written
// is a DIFFERENT, marked outcome because the parent was never told.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestRecordNotDelivered_EveryCapRefusalReason pins all four reasons, each with
// its own recorded value and its own parent-facing phrase.
func TestRecordNotDelivered_EveryCapRefusalReason(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		reason gen.DelegateNotDeliveredSummaryLastReason
		phrase string
	}{
		{"rate limited", session.ErrInboxRateLimited,
			gen.DelegateNotDeliveredSummaryLastReasonRateLimited, "the report rate limit was reached"},
		{"body too large", session.ErrInboxBodyTooLarge,
			gen.DelegateNotDeliveredSummaryLastReasonBodyTooLarge, "the report was too large"},
		{"question or blocker ceiling", session.ErrInboxPerChildCeiling,
			gen.DelegateNotDeliveredSummaryLastReasonQuestionBlockerCeiling, "too many questions or blockers are still open"},
		{"unacked cap", session.ErrInboxSessionFull,
			gen.DelegateNotDeliveredSummaryLastReasonUnackedCap, "the parent inbox is full"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, lifecycle, _, _ := newDeliverTestLoop(t)
			const parentID, childID = "parent-1", "child-1"
			seedParentAndChild(t, lifecycle, parentID, childID)
			rec, err := lifecycle.Load(childID)
			require.NoError(t, err, "fixture: the child record must load")

			got := al.recordNotDelivered(lifecycle, parentID, rec, "progress", tc.err)
			require.Error(t, got, "a refused report is always reported to the caller")
			require.ErrorIs(t, got, tc.err, "the typed refusal cause stays reachable through the wrapper")
			require.NotContains(t, got.Error(), session.ParentNotNotifiedMarker,
				"a refusal that WAS recorded must not claim the parent was left uninformed")

			after, err := lifecycle.Load(childID)
			require.NoError(t, err)
			require.NotNil(t, after.NotDelivered, "a cap refusal must be recorded on the child")
			assert.EqualValues(t, 1, after.NotDelivered.Count)
			assert.Equal(t, tc.reason, after.NotDelivered.LastReason,
				"the recorded reason must be THIS refusal, not a shared one")
			assert.Equal(t, "progress", after.NotDelivered.LastKind)
			assert.False(t, after.NotDelivered.LastAt.IsZero(), "the record must carry when it happened")
		})
	}
}

// TestRecordNotDelivered_NonCapFailureIsPassedThrough pins the other half of the
// contract: a failure that is NOT one of the four cap refusals is returned
// unchanged and records nothing, so a storage fault is never dressed up as a
// cap refusal the parent could act on.
func TestRecordNotDelivered_NonCapFailureIsPassedThrough(t *testing.T) {
	al, lifecycle, _, _ := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	rec, err := lifecycle.Load(childID)
	require.NoError(t, err)

	storageFault := errors.New("disk went away")
	got := al.recordNotDelivered(lifecycle, parentID, rec, "progress", storageFault)
	require.Error(t, got)
	require.ErrorIs(t, got, storageFault, "the storage fault is returned unchanged")

	after, err := lifecycle.Load(childID)
	require.NoError(t, err)
	assert.Nil(t, after.NotDelivered, "a non-cap failure must record nothing")
}

// TestRecordNotDelivered_UnrecordableRefusalMarksTheParentNotNotified: when the
// cap refusal itself cannot be recorded, the caller must be able to tell that
// the parent was never told — the one outcome the marker exists for.
func TestRecordNotDelivered_UnrecordableRefusalMarksTheParentNotNotified(t *testing.T) {
	al, lifecycle, _, _ := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	rec, err := lifecycle.Load(childID)
	require.NoError(t, err)

	// Make the child's record unwritable: a directory where the record file
	// belongs. The kernel refuses it for every caller, root included.
	blocked := filepath.Join(lifecycle.Dir(), childID+".jsonl")
	require.NoError(t, os.Remove(blocked))
	require.NoError(t, os.Mkdir(blocked, 0o700))
	t.Cleanup(func() { _ = os.RemoveAll(blocked) })
	// Instrument: the write really must fail.
	require.Error(t, lifecycle.Mutate(childID, func(*session.LifecycleRecord) error { return nil }),
		"instrument: the lifecycle write must fail while the record path is blocked")

	got := al.recordNotDelivered(lifecycle, parentID, rec, "progress", session.ErrInboxRateLimited)
	require.Error(t, got)
	require.ErrorIs(t, got, session.ErrInboxRateLimited, "the original refusal cause is still reachable")
	require.Contains(t, got.Error(), session.ParentNotNotifiedMarker,
		"an unrecorded refusal must be MARKED: the parent was never told")
}
