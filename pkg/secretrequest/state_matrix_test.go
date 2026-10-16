package secretrequest_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// BDD-6.4/6.10 and chief Q1 A: consumed is internal bookkeeping,
// never a replacement for the externally visible submitted state.
func TestSecretMatrix_SubmittedAndClaimedStaySubmittedValueFree(t *testing.T) {
	f := newMatrixEnv(t)
	pending := f.pending()
	got, err := f.reg.Get(pending.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	require.Equal(t, "pending", got.Card.State)
	require.Nil(t, secretrequest.State(got).ReferenceNames)
	submitted := submitMatrix(t, f, pending)
	require.Len(t, f.attempts, 1)
	require.Equal(t, submitted.Card, f.attempts[0].Card, "dispatch arguments must preserve id, session and references")
	claim, accepted, err := f.reg.ClaimResume(pending.Card.RequestId, pending.Card.SessionId)
	require.NoError(t, err)
	require.True(t, accepted, "first durable claim must be accepted")
	require.Equal(t, submitted.Card, claim.Card)
	_, accepted, err = f.reg.ClaimResume(pending.Card.RequestId, pending.Card.SessionId)
	require.NoError(t, err)
	require.False(t, accepted, "second claim for the same request id must not start a turn")
	got, err = f.reg.Get(pending.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	require.Equal(t, "submitted", got.Card.State, "K5 must never expose consumed")
	require.Equal(t, submitted.Card.ReferenceNames, got.Card.ReferenceNames)
	_, err = f.reg.Submit(pending.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": "FAKE-REPLAY-MUST-NOT-WIN"}))
	requireMatrixFailure(t, err, http.StatusConflict, "submitted")
	value, err := f.store.Get(submitted.Card.ReferenceNames[0])
	require.NoError(t, err)
	require.Equal(t, matrixValue, value)
	snapshot := f.reg.Snapshot(matrixOwner, pending.Card.SessionId)
	require.Len(t, snapshot, 1)
	require.Equal(t, submitted.Card, snapshot[0])
	transcript, err := f.sessions.ReadTranscript(pending.Card.SessionId)
	require.NoError(t, err)
	require.Len(t, transcript, 1)
	require.Equal(t, "secret-resume-"+pending.Card.RequestId, transcript[0].ID)
	require.Equal(t, "user", transcript[0].Role)
	require.Contains(t, transcript[0].Content, submitted.Card.ReferenceNames[0])
	for label, data := range map[string]any{"GET": secretrequest.State(got), "snapshot": snapshot, "resume": f.attempts, "transcript": transcript} {
		requireValueFree(t, label, data, matrixValue, "FAKE-REPLAY-MUST-NOT-WIN")
	}
	require.NoError(t, f.reg.RetryResumes())
	require.Len(t, f.attempts, 1, "attach after consumption must not inject again")
}

// BDD-6.5/6.16: cancellation and Stop before submit are terminal and store nothing.
func TestSecretMatrix_CancelAndStopBeforeSubmitWriteNothing(t *testing.T) {
	for _, event := range []string{"cancelled", "stopped"} {
		t.Run(event, func(t *testing.T) {
			f := newMatrixEnv(t)
			rec := f.pending()
			if event == "cancelled" {
				out, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, cancelInput())
				require.NoError(t, err)
				require.Equal(t, event, out.Card.State)
			} else {
				require.NoError(t, f.reg.Stop(rec.Card.SessionId))
			}
			got, err := f.reg.Get(rec.Card.RequestId, matrixOwner)
			require.NoError(t, err)
			require.Equal(t, event, got.Card.State)
			require.Empty(t, got.Card.ReferenceNames)
			_, err = f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": matrixValue}))
			requireMatrixFailure(t, err, http.StatusConflict, event)
			requireNoMatrixCommit(t, f, rec.Card.RequestId)
			require.Equal(t, []gen.SessionStateSecretRequest{got.Card}, f.reg.Snapshot(matrixOwner, rec.Card.SessionId))
		})
	}
}

// FR-067: Stop after submit does not roll back a durable, not-yet-selected value.
func TestSecretMatrix_StopAfterSubmitPreservesStoredVersion(t *testing.T) {
	f := newMatrixEnv(t)
	rec := submitMatrix(t, f, f.pending())
	require.NoError(t, f.reg.Stop(rec.Card.SessionId))
	f.stopped[rec.Card.SessionId] = true
	got, err := f.reg.Get(rec.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	require.Equal(t, rec.Card, got.Card)
	value, err := f.store.Get(rec.Card.ReferenceNames[0])
	require.NoError(t, err)
	require.Equal(t, matrixValue, value)
	_, accepted, err := f.reg.ClaimResume(rec.Card.RequestId, rec.Card.SessionId)
	require.ErrorContains(t, err, "stopped")
	require.False(t, accepted)
	require.NoError(t, f.reg.RetryResumes())
	require.Len(t, f.attempts, 1, "stopped sessions must not be woken again")
}

// Chief Q1 A / FR-066: a card does not auto-expire. Virtual time tests the
// production time package; no production timer or registry state is replaced.
func TestSecretMatrix_PendingHasNoAutoExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newMatrixEnv(t)
		rec := f.pending()
		// A year is arbitrary inactivity, not an invented expiry threshold.
		time.Sleep(365 * 24 * time.Hour)
		got, err := f.reg.Get(rec.Card.RequestId, matrixOwner)
		require.NoError(t, err)
		require.Equal(t, rec.Card, got.Card, "unanswered request stays pending with no activity")
		submitMatrix(t, f, rec)
	})
}

// BDD-6.16 / §6.7: another account cannot learn state or store values.
func TestSecretMatrix_WrongOwnerCannotReadSubmitOrSnapshot(t *testing.T) {
	f := newMatrixEnv(t)
	rec := f.pending()
	_, err := f.reg.Get(rec.Card.RequestId, "other-owner")
	requireMatrixFailure(t, err, http.StatusNotFound, "")
	_, err = f.reg.Submit(rec.Card.RequestId, "other-owner", valuesInput(map[string]string{"api_key": matrixValue}))
	requireMatrixFailure(t, err, http.StatusNotFound, "")
	require.Empty(t, f.reg.Snapshot("other-owner", rec.Card.SessionId))
	requireNoMatrixCommit(t, f, rec.Card.RequestId)
	// Passing control prevents an empty/incorrect ownership instrument looking green.
	got, err := f.reg.Get(rec.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	require.Equal(t, "pending", got.Card.State)
}

// BDD-6.16: session deletion removes reachability and never recreates a session.
func TestSecretMatrix_DeletedSessionCannotSubmitOrSnapshot(t *testing.T) {
	f := newMatrixEnv(t)
	rec := f.pending()
	require.NoError(t, f.sessions.DeleteSession(rec.Card.SessionId))
	requireMatrixNotFound(t, f.reg, rec.Card.RequestId)
	_, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": matrixValue}))
	requireMatrixFailure(t, err, http.StatusNotFound, "")
	require.Empty(t, f.reg.Snapshot(matrixOwner, rec.Card.SessionId))
	requireNoMatrixCommit(t, f, rec.Card.RequestId)
	_, err = os.Stat(filepath.Join(f.sessions.BaseDir(), rec.Card.SessionId))
	require.ErrorIs(t, err, os.ErrNotExist, "submit must not recreate deleted session metadata")
	boot := secretrequest.NewRegistry(f.options())
	require.NoError(t, boot.Recover())
	requireMatrixNotFound(t, boot, rec.Card.RequestId)
}

// BDD-6.11/6.12 and FR-078: helper ownership is derived through real ancestry.
func TestSecretMatrix_HelperCardRoutesToOmnipusMainOnly(t *testing.T) {
	for _, rootAgent := range []string{"omnipus", "jim"} {
		t.Run(rootAgent, func(t *testing.T) {
			f := newMatrixEnv(t)
			var root string
			if rootAgent == "omnipus" {
				main, err := f.sessions.GetOrCreateMainSession(matrixWorkspace, rootAgent)
				require.NoError(t, err)
				owner := matrixOwner
				require.NoError(t, f.sessions.SetMeta(main.ID, session.MetaPatch{Owner: &owner}))
				root = main.ID
			} else {
				root = f.newSession(rootAgent, "")
			}
			child := f.newSession("helper", root)
			rec, err := f.reg.Create(context.Background(), child, "helper", "helper-chat", "", providerInput())
			if rootAgent == "jim" {
				require.EqualError(t, err, "ask your parent to request a secret")
				require.Empty(t, f.reg.Snapshot(matrixOwner, root))
				requireNoMatrixCommit(t, f, rec.Card.RequestId)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, rec.Card.RootSessionId)
			require.Equal(t, root, *rec.Card.RootSessionId)
			require.Equal(t, "helper", rec.Card.ActingAgentId)
			require.Equal(t, "Setup helper", rec.Card.ActingAgentName)
			require.Equal(t, []gen.SessionStateSecretRequest{rec.Card}, f.reg.Snapshot(matrixOwner, root))
			require.NoError(t, f.reg.Stop(root))
			got, err := f.reg.Get(rec.Card.RequestId, matrixOwner)
			require.NoError(t, err)
			require.Equal(t, "stopped", got.Card.State, "root Stop terminates its helper's pending card")
			requireNoMatrixCommit(t, f, rec.Card.RequestId)
		})
	}
}
