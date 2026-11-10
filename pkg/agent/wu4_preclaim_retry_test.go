package agent

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SF-02 / FR-087: bus acceptance is not durable consumption. Only ClaimResume
// consumes the request id; a rejection before it must permit attach to retry.
func (h *wu4PreclaimFixture) requireUnclaimed(t *testing.T) {
	t.Helper()
	require.Zero(t, secretWakeClaimCount(t, h.f.al, h.record), "instrument: selected failure occurred BEFORE the durable claim")
	require.Len(t, h.f.provider.calls(h.f.root), 1, "instrument: only the original parked ordinary turn ran")
	h.requireSavedUnchanged(t)
}

func (h *wu4PreclaimFixture) requireOneContinuation(t *testing.T) {
	t.Helper()
	calls := h.f.provider.calls(h.f.root)
	claims := secretWakeClaimCount(t, h.f.al, h.record)
	t.Logf("SF-02 actual continuation/claim counts after retry: %d/%d; want 1/1", len(calls)-1, claims)
	assert.Len(t, calls, 2, "SF-02: the original park plus exactly ONE real continuation must run after repair")
	assert.Equal(t, 1, claims, "FR-087: the retry must publish exactly ONE durable request-id claim")
	if len(calls) == 2 {
		requireSecretWakePrompt(t, calls[1], h.record)
	}
	h.requireSavedUnchanged(t)
}

// Same live registry throughout fault -> repair -> repeated attach. In
// particular, this helper does NOT recreate it to erase the in-flight latch.
func (h *wu4PreclaimFixture) retryAfterRepair(t *testing.T) {
	t.Helper()
	reg := h.f.al.SecretRequests()
	for range 2 { // The second attach must not dispatch a duplicate of the first.
		require.NoError(t, reg.RetryResumes())
	}
	wakes := h.takeRetryWakes()
	assert.Len(t, wakes, 1, "SF-02: a repaired PRE-CLAIM rejection must not remain stranded behind transport inFlight")
	for _, wake := range wakes {
		require.Equal(t, h.record.Card.RequestId, wake.Metadata[secretResumeMetadataKey])
		require.NoError(t, h.process(wake), "run the actual ordinary continuation received from retry, not a direct claim")
	}
	h.requireOneContinuation(t)
	for range 2 {
		require.NoError(t, reg.RetryResumes())
	}
	assert.Empty(t, h.takeRetryWakes(), "later attaches never reset or replay a durable claim")
	h.requireOneContinuation(t)
}

func TestWU4SF02RegistryReadRejectionRemainsRetryableAfterRepair(t *testing.T) {
	h := newWU4PreclaimFixture(t, false)
	store := h.f.al.GetSessionStore()
	require.NoError(t, store.FlushAndEvictSessionMeta(h.f.root))
	path := filepath.Join(store.BaseDir(), h.f.root, "meta.json")
	cut := wu4PreclaimBlockFile(t, path)
	// The public eviction seam forces a real disk read, not a warm-cache false
	// success. The live registry itself, and its accepted transport, stay intact.
	_, probeErr := h.f.al.SecretRequests().Get(h.record.Card.RequestId, h.record.Owner)
	var refusal *secretrequest.RequestError
	require.ErrorAs(t, probeErr, &refusal)
	require.Equal(t, http.StatusNotFound, refusal.Status)
	require.Equal(t, "secret request not found", refusal.Message)
	consumeErr := h.process(h.original)
	require.NoError(t, cut.restore())
	store.EvictSessionMeta(h.f.root)
	require.ErrorAs(t, consumeErr, &refusal)
	require.Equal(t, http.StatusNotFound, refusal.Status)
	require.Equal(t, "secret request not found", refusal.Message)
	h.requireUnclaimed(t)
	h.retryAfterRepair(t)
}

func TestWU4SF02LifecycleReadRejectionRemainsRetryableAfterRepair(t *testing.T) {
	h := newWU4PreclaimFixture(t, false)
	path := filepath.Join(h.f.al.GetSessionLifecycleStore().Dir(), h.f.root+".jsonl")
	cut := wu4PreclaimBlockFile(t, path)
	_, probeErr := h.f.al.GetSessionLifecycleStore().Load(h.f.root)
	var pathErr *os.PathError
	require.ErrorAs(t, probeErr, &pathErr)
	require.Equal(t, path, pathErr.Path, "instrument: fail THIS request's actual lifecycle read")
	consumeErr := h.process(h.original)
	require.NoError(t, cut.restore())
	require.ErrorAs(t, consumeErr, &pathErr)
	require.Equal(t, path, pathErr.Path)
	require.ErrorContains(t, consumeErr, "session: lifecycle:")
	h.requireUnclaimed(t)
	h.retryAfterRepair(t)
}

func TestWU4SF02RestartRevivalRejectionRemainsRetryableAfterRepair(t *testing.T) {
	h := newWU4PreclaimFixture(t, true)
	h.failRevivalRead = true
	consumeErr := h.process(h.original)
	require.True(t, h.revivalReadReached, "instrument: the real restart revival must invoke its existing state-writer dependency")
	require.NoError(t, h.revivalCut.restore())
	var pathErr *os.PathError
	require.ErrorAs(t, consumeErr, &pathErr)
	require.Equal(t, h.revivalCut.path, pathErr.Path)
	require.ErrorContains(t, consumeErr, "steer: write revival state", "the rejection must come from the real writer, not a fake failed revive")
	current, err := h.f.al.GetSessionLifecycleStore().Load(h.f.root)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleQueued, current.State, "instrument: revival itself committed; its later real read failed before ClaimResume")
	h.requireUnclaimed(t)
	h.retryAfterRepair(t)
}

func wu4PreclaimClaims(t *testing.T, h *wu4PreclaimFixture) []session.TranscriptEntry {
	t.Helper()
	entries, err := h.f.al.GetSessionStore().ReadTranscript(h.f.root)
	require.NoError(t, err)
	var claims []session.TranscriptEntry
	for _, entry := range entries {
		if entry.ID == secretrequest.ResumeEntryID(h.record.Card.RequestId) {
			claims = append(claims, entry)
		}
	}
	return claims
}

func TestWU4SF02ConsumedOrdinaryRequestNeverResetsDurableClaim(t *testing.T) {
	h := newWU4PreclaimFixture(t, false)
	require.NoError(t, h.process(h.original))
	h.requireOneContinuation(t)
	claims := wu4PreclaimClaims(t, h)
	require.Len(t, claims, 1)
	for range 2 {
		require.NoError(t, h.f.al.SecretRequests().RetryResumes())
		require.NoError(t, h.process(h.original), "duplicate accepted transport is a no-op after the real claim")
	}
	require.Empty(t, h.takeRetryWakes())
	// Only AFTER successful consumption, drop process-local registry state to
	// prove that the saved claim, not the old in-flight latch, prevents replay.
	reg := h.f.freshRegistry(t)
	for range 2 {
		require.NoError(t, reg.RetryResumes())
	}
	require.Empty(t, h.takeRetryWakes())
	require.NoError(t, h.process(h.original))
	h.requireOneContinuation(t)
	require.Equal(t, claims, wu4PreclaimClaims(t, h), "retry must not rewrite, reset or replace the original durable claim")
}

func TestWU4SF02ExplicitStopVetoSurvivesAttachAndSessionRevival(t *testing.T) {
	for _, actor := range []steer.Principal{
		{Kind: steer.PrincipalKindHuman, ID: "qa-secret-wake-owner"},
		{Kind: steer.PrincipalKindAgent, ID: "omnipus"},
	} {
		t.Run(string(actor.Kind), func(t *testing.T) {
			h := newWU4PreclaimFixture(t, true)
			result, err := h.f.al.StopSession(context.Background(), StopRequest{SessionID: h.f.root, By: actor, Channel: "webchat"})
			require.NoError(t, err)
			require.NoError(t, result.RootErr)
			require.Empty(t, result.Report.Unreachable)
			require.True(t, result.Durable)
			for range 2 {
				require.NoError(t, h.f.al.SecretRequests().RetryResumes())
			}
			require.Empty(t, h.takeRetryWakes())
			require.ErrorContains(t, h.process(h.original), "secret continuation binding is unavailable")
			h.requireUnclaimed(t)
			stopped, err := h.f.al.GetSessionLifecycleStore().Load(h.f.root)
			require.NoError(t, err)
			require.True(t, stopped.Stopped(), "instrument: a real stopped session precedes public revival")
			_, err = h.f.al.steerCanceller().Revive(context.Background(), h.f.root,
				steer.Principal{Kind: steer.PrincipalKindHuman, ID: "qa-secret-wake-owner"})
			require.NoError(t, err)
			current, err := h.f.al.GetSessionLifecycleStore().Load(h.f.root)
			require.NoError(t, err)
			require.False(t, current.Stopped(), "instrument: session revival really removes the session-level fence")
			reg := h.f.freshRegistry(t)
			for range 2 {
				require.NoError(t, reg.RetryResumes())
			}
			require.Empty(t, h.takeRetryWakes(), "explicit request Stop must survive session revival and actual metadata reload")
			require.ErrorContains(t, h.process(h.original), "secret continuation binding is unavailable")
			stored, err := reg.Get(h.record.Card.RequestId, h.record.Owner)
			require.NoError(t, err)
			require.True(t, stored.ResumeStopped, "retry must NEVER clear the durable request-level Stop veto")
			h.requireUnclaimed(t)
		})
	}
}
