package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Round 5 item 1: invoke the actual ordinary admission wrapper. A competing
// durable generation wins at the existing publication boundary; the real
// WriteSteerRevivalState rejects its stale generation, not an injected error.
// This pins the caller's %w, rather than reconstructing that wrap in the test.
func TestI1R5AdmissionPreservesStaleRevivalCause(t *testing.T) {
	h := i1R3RestartStopped(t)
	ls := h.al.GetSessionLifecycleStore()
	selectedGeneration := h.load(t).Generation
	publications := 0
	canceller := NewSteerCanceller(ls).SetRevivalStateWriter(func(ctx context.Context, id string, generation int) error {
		publications++
		require.Equal(t, h.id, id)
		require.Equal(t, selectedGeneration, generation)
		admitted, err := ls.Load(id)
		require.NoError(t, err)
		require.Equal(t, session.LifecycleQueued, admitted.State, "actual atomic revival must precede publication")
		require.NotNil(t, admitted.ExecutionID)
		require.NoError(t, ls.Mutate(id, func(rec *session.LifecycleRecord) error {
			rec.Generation++
			rec.State = session.LifecycleRunning
			rec.ExecutionID = &session.ExecutionIdentity{RunID: "i1-r5-newer-run", BootSeq: h.al.bootEpochFor()}
			return nil
		}))
		return h.al.WriteSteerRevivalState(ctx, id, generation)
	})
	h.al.SetSteerCanceller(canceller)
	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "d2b-owner"}
	prepared, err := h.al.prepareOrdinarySessionExecution(context.Background(), h.id,
		processOptions{SessionKey: "agent:" + testDefaultAgentID + ":session:" + h.id,
			TranscriptSessionID: h.id, TranscriptStore: h.al.GetSessionStore()}, &by)
	require.ErrorIs(t, err, steer.ErrStaleGeneration, "the actual admission wrap must preserve the selection refusal")
	var ordinary *ordinaryAdmissionRefusalError
	require.ErrorAs(t, err, &ordinary)
	assert.ErrorContains(t, err, "ordinary admission: explicit revival failed:", "the production caller must be the wrapping site")
	const guidance = "Your previous reply is still finishing — send your message again in a moment."
	assert.Equal(t, guidance, userVisibleTurnError(err))
	assert.Equal(t, 1, publications)
	assert.Nil(t, prepared.execution)
	current := h.load(t)
	assert.Equal(t, selectedGeneration+1, current.Generation)
	assert.Equal(t, "i1-r5-newer-run", current.ExecutionID.RunID)
	assert.Empty(t, h.provider.calls())
}

// Silent E / already-passing storage-path control: a real store read error
// remains a genuine revival failure. This is not a raw append/Write/Sync test.
func TestI1R4RealStorageRefusalRemainsRevivalFailure(t *testing.T) {
	h := i1R3RestartStopped(t)
	selected, before := h.load(t), h.journal(t)
	path := filepath.Join(h.al.GetSessionLifecycleStore().Dir(), h.id+".jsonl")
	backup := path + ".i1-r4-saved"
	require.NoError(t, os.Rename(path, backup))
	require.NoError(t, os.Mkdir(path, 0o700))
	restore := func() {
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Rename(backup, path))
	}
	t.Cleanup(func() {
		if _, err := os.Stat(backup); err == nil {
			restore()
		}
	})
	refused := h.al.reviveOrdinaryRecordWithExecution(context.Background(), selected,
		session.ExecutionIdentity{RunID: "storage-refused-revival", BootSeq: h.al.bootEpochFor()}, steer.Principal{})
	var pathErr *os.PathError
	require.ErrorAs(t, refused, &pathErr)
	assert.False(t, errors.Is(refused, steer.ErrStaleGeneration), "filesystem failure cannot become a lost selection")
	failure, remembered := h.al.lastRevivalFailure(h.id)
	require.True(t, remembered, "actual store error must remain available to the launch backstop")
	assert.Equal(t, refused, failure.cause)
	assert.Empty(t, h.provider.calls())
	restore()
	assert.Equal(t, before, h.journal(t))
}

// Silent D / accepted control: a later stale selection clears an older genuine
// diagnostic too. This is the founder/dispatcher-accepted latest-attempt policy.
func TestI1R4StaleSelectionClearsOlderFailure(t *testing.T) {
	h := i1R3RestartStopped(t)
	prior := errors.New("earlier genuine storage failure")
	h.al.markRevivalFailure(h.id, prior)
	failure, remembered := h.al.lastRevivalFailure(h.id)
	require.True(t, remembered)
	require.Equal(t, prior, failure.cause)
	h.al.markRevivalFailure(h.id, fmt.Errorf("newer selection: %w", steer.ErrStaleGeneration))
	_, remembered = h.al.lastRevivalFailure(h.id)
	assert.False(t, remembered, "accepted policy: the newer selection refusal supersedes older diagnostic memory")
}
