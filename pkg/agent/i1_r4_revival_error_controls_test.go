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

// Gap 1 / already-passing control: wrapping the real ordinary revival refusal
// preserves its cause and its existing human guidance. No full-entry race hook
// is needed to prove that the special admission-level return is redundant.
func TestI1R4StaleRevivalWrappingIsRedundant(t *testing.T) {
	h := i1R3RestartStopped(t)
	ls := h.al.GetSessionLifecycleStore()
	selected := h.load(t)
	require.NoError(t, ls.Mutate(h.id, func(rec *session.LifecycleRecord) error {
		rec.ExecutionID = &session.ExecutionIdentity{RunID: "newer-selected-execution", BootSeq: h.al.bootEpochFor()}
		return nil
	}))
	before := h.journal(t)
	refused := h.al.reviveOrdinaryRecordWithExecution(context.Background(), selected,
		session.ExecutionIdentity{RunID: "losing-revival", BootSeq: h.al.bootEpochFor()})
	require.ErrorIs(t, refused, steer.ErrStaleGeneration)
	wrapped := fmt.Errorf("ordinary admission: explicit revival failed: %w", refused)
	require.ErrorIs(t, wrapped, steer.ErrStaleGeneration, "wrapping preserves the selection cause")
	var ordinary *ordinaryAdmissionRefusalError
	require.ErrorAs(t, wrapped, &ordinary, "errors.As in the real translator reaches the ordinary refusal through any wrapper")
	const guidance = "Your previous reply is still finishing — send your message again in a moment."
	assert.Equal(t, guidance, userVisibleTurnError(wrapped))
	assert.Equal(t, before, h.journal(t))
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
		session.ExecutionIdentity{RunID: "storage-refused-revival", BootSeq: h.al.bootEpochFor()})
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
