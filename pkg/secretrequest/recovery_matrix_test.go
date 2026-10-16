package secretrequest_test

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/require"
)

// FR-087 / BDD-6.24 cut before publication: no receipt means interrupted,
// never submitted; repeated Recover on the same registry changes nothing.
func TestSecretRecovery_NoReceiptInterruptsWithoutResume(t *testing.T) {
	f := newMatrixEnv(t)
	rec := f.pending()
	boot := secretrequest.NewRegistry(f.options())
	require.NoError(t, boot.Recover())
	got, err := boot.Get(rec.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	require.Equal(t, "interrupted", got.Card.State)
	require.Empty(t, got.Card.ReferenceNames)
	require.NoError(t, boot.Recover())
	require.NoError(t, boot.RetryResumes())
	requireNoMatrixCommit(t, f, rec.Card.RequestId)
	_, err = boot.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": matrixValue}))
	requireMatrixFailure(t, err, http.StatusConflict, "interrupted")
	require.Equal(t, got.Card, boot.Snapshot(matrixOwner, rec.Card.SessionId)[0])
}

// The credential store is real. Direct CommitCard is the public publication
// seam; session metadata deliberately remains the original pending snapshot,
// exactly the crash cut after durable publication and before terminal metadata.
func TestSecretRecovery_ReceiptWinsOverPendingMetadata(t *testing.T) {
	f := newMatrixEnv(t)
	rec := f.pending()
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	receipt, err := f.store.CommitCard(rec.Card.RequestId, rec.Card.SessionId, []credentials.CardValue{{
		BaseName: "openai_api_key", Recipient: rec.Recipients[0], Value: matrixValue,
	}}, at)
	require.NoError(t, err)
	require.Equal(t, []string{"openai_api_key.v1"}, receipt.References)
	boot := secretrequest.NewRegistry(f.options())
	require.NoError(t, boot.Recover())
	got, err := boot.Get(rec.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	require.Equal(t, "submitted", got.Card.State)
	require.Equal(t, receipt.References, got.Card.ReferenceNames)
	require.Equal(t, at, secretrequest.State(got).UpdatedAt)
	require.NoError(t, boot.RetryResumes())
	require.NoError(t, boot.RetryResumes())
	require.Len(t, f.attempts, 1)
	require.Equal(t, got.Card, f.attempts[0].Card)
	_, claimed, err := boot.ClaimResume(rec.Card.RequestId, rec.Card.SessionId)
	require.NoError(t, err)
	require.True(t, claimed)
	// Another boot must see the durable request-id marker, not reinject.
	nextBoot := secretrequest.NewRegistry(f.options())
	require.NoError(t, nextBoot.Recover())
	require.NoError(t, nextBoot.RetryResumes())
	require.Len(t, f.attempts, 1)
	_, claimed, err = nextBoot.ClaimResume(rec.Card.RequestId, rec.Card.SessionId)
	require.NoError(t, err)
	require.False(t, claimed)
	requireValueFree(t, "recovered snapshot", nextBoot.Snapshot(matrixOwner, rec.Card.SessionId), matrixValue)
	requireValueFree(t, "recovered dispatch", f.attempts, matrixValue)
}

// FR-087 / BDD-6.24: metadata write failure is not a failed credential commit.
// Filesystem fault is at the process edge: an existing directory cannot be
// replaced by the atomic file publisher on any supported OS.
func TestSecretRecovery_TerminalMetadataFailureRecoversCommittedReceipt(t *testing.T) {
	f := newMatrixEnv(t)
	rec := f.pending()
	raw, err := f.sessions.ReadSecretRequestState(rec.Card.SessionId)
	require.NoError(t, err)
	path := filepath.Join(f.sessions.BaseDir(), rec.Card.SessionId, "secret_requests.json")
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Mkdir(path, 0o700))
	out, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": matrixValue}))
	requireMatrixFailure(t, err, http.StatusServiceUnavailable, "submitted")
	require.Equal(t, "submitted", out.Card.State)
	receipt, exists, err := f.store.CardReceipt(rec.Card.RequestId)
	require.NoError(t, err)
	require.True(t, exists, "committed receipt survives terminal metadata failure")
	require.Len(t, receipt.References, 1)
	require.Equal(t, out.Card.ReferenceNames, receipt.References)
	stored, err := f.store.Get(receipt.References[0])
	require.NoError(t, err)
	require.Equal(t, matrixValue, stored)
	// Restore only the pre-publication metadata, as a fresh process would see
	// if terminal publication failed. The credentials file is never edited.
	require.NoError(t, os.Remove(path))
	require.NoError(t, f.sessions.WriteSecretRequestState(rec.Card.SessionId, raw))
	boot := secretrequest.NewRegistry(f.options())
	require.NoError(t, boot.Recover())
	got, err := boot.Get(rec.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	require.Equal(t, out.Card, got.Card)
	_, claimed, err := boot.ClaimResume(rec.Card.RequestId, rec.Card.SessionId)
	require.NoError(t, err)
	require.True(t, claimed)
	_, claimed, err = f.reg.ClaimResume(rec.Card.RequestId, rec.Card.SessionId)
	require.NoError(t, err)
	require.False(t, claimed, "queued message from old process cannot consume again")
	require.NoError(t, boot.RetryResumes())
	require.Len(t, f.attempts, 1, "already claimed id is never injected again")
}

// FR-087: transport failure is retriable; a successful accepted continuation
// stays keyed by request id across attach, concurrency and a later restart.
func TestSecretRecovery_FailedDispatchRetriesAndClaimsExactlyOnce(t *testing.T) {
	f := newMatrixEnv(t)
	rec := f.pending()
	f.resumeErr = errors.New("synthetic transport failure")
	out, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": matrixValue}))
	requireMatrixFailure(t, err, http.StatusServiceUnavailable, "submitted")
	require.Equal(t, "submitted", out.Card.State)
	require.Len(t, f.attempts, 1)
	f.resumeErr = nil
	boot := secretrequest.NewRegistry(f.options())
	require.NoError(t, boot.Recover())
	require.NoError(t, boot.RetryResumes())
	require.NoError(t, boot.RetryResumes())
	require.Len(t, f.attempts, 2, "one failed dispatch plus one successful retry, not a repeated successful dispatch")
	require.Equal(t, out.Card, f.attempts[1].Card)
	type result struct {
		claimed bool
		err     error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, claimed, claimErr := boot.ClaimResume(rec.Card.RequestId, rec.Card.SessionId)
			results <- result{claimed, claimErr}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	claims := 0
	for result := range results {
		require.NoError(t, result.err)
		if result.claimed {
			claims++
		}
	}
	require.Equal(t, 1, claims, "two resumes for one request id must be impossible")
	nextBoot := secretrequest.NewRegistry(f.options())
	require.NoError(t, nextBoot.Recover())
	require.NoError(t, nextBoot.RetryResumes())
	require.Len(t, f.attempts, 2)
	transcript, err := f.sessions.ReadTranscript(rec.Card.SessionId)
	require.NoError(t, err)
	require.Len(t, transcript, 1)
	require.Equal(t, "secret-resume-"+rec.Card.RequestId, transcript[0].ID)
	require.Contains(t, transcript[0].Content, out.Card.ReferenceNames[0])
	requireValueFree(t, "durable resume", transcript, matrixValue)
}

// FR-084 and BDD-6.16: store failure leaves the request retryable and does not
// expose error text from the filesystem (which could itself contain values).
func TestSecretRecovery_StoreWriteFailureKeepsPendingAndWritesNothing(t *testing.T) {
	f := newMatrixEnv(t)
	rec := f.pending()
	// Fresh key unlock creates no file. The destination directory is an
	// actual write fault rather than a mock Credentials callback.
	require.NoError(t, os.Mkdir(f.store.Path(), 0o700))
	_, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": matrixValue}))
	requireMatrixFailure(t, err, http.StatusInternalServerError, "")
	got, err := f.reg.Get(rec.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	require.Equal(t, rec.Card, got.Card)
	require.Empty(t, f.attempts)
	require.NoError(t, os.Remove(f.store.Path()))
	requireNoMatrixCommit(t, f, rec.Card.RequestId)
	submitMatrix(t, f, rec)
}
