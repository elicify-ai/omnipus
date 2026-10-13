package agent

import (
	"errors"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// §4.2: steering/launch return the model-write failure even when their
// following transcript/metadata writes would succeed. The index-only fault
// leaves those paths writable, so a second error cannot mask the ignored one.
func TestWU22SteeredInstructionArchiveFailureReachesCaller(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	restore := wu22BlockModelWrite(t, h.store, h.sid)
	err := h.al.appendSteeredInstruction(h.sid, h.agent.ID, "WU22 steered instruction", false)
	var cause *os.PathError
	assert.ErrorAs(t, err, &cause, "§4.2: steering must return the failed model write instead of false success")
	if cause != nil {
		assert.Contains(t, cause.Path, "ordinal_rows.log")
	}
	h.assertSiblingWritable(t) // no repair before proving isolation
	restore()
	assert.Empty(t, h.store.GetHistory(h.sid), "missing steering instruction must not be reported model-visible")
}

func TestWU22SteeredInstructionHealthyStoreRecordsBothViews(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	require.NoError(t, h.al.appendSteeredInstruction(h.sid, h.agent.ID, "WU22 steered instruction", false))
	assert.Equal(t, []providers.Message{{Role: "user", Content: "WU22 steered instruction"}}, h.store.GetHistory(h.sid))
	entries, err := h.store.ReadTranscript(h.sid)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "user", entries[0].Role)
	assert.Equal(t, "WU22 steered instruction", entries[0].Content)
	assert.Equal(t, h.agent.ID, entries[0].AgentID)
	assert.Contains(t, entries[0].ID, h.sid+"-instruction-")
	h.assertSiblingWritable(t)
}

func TestWU22ChildHistoryArchiveFailureReachesLauncher(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	launcher := NewSteerLauncher(h.al)
	restore := wu22BlockModelWrite(t, h.store, h.sid)
	err := launcher.writeChildMetaAndHistory(h.store, h.sid, "WU22 child", "", "", "", steer.LaunchRequest{
		TargetAgentID: h.agent.ID, Task: "WU22 child instruction",
	})
	assert.ErrorIs(t, err, steer.ErrStoreWrite, "mandatory first-message archive failure must refuse launch (§4.2)")
	var cause *os.PathError
	assert.ErrorAs(t, err, &cause, "launch must retain original write cause")
	h.assertSiblingWritable(t) // no repair before proving isolation
	restore()
	assert.Empty(t, h.store.GetHistory(h.sid), "child has no model-visible first instruction")
}

func TestWU22ChildHistoryHealthyStoreSeedsLaunch(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	launcher := NewSteerLauncher(h.al)
	require.NoError(t, launcher.writeChildMetaAndHistory(h.store, h.sid, "WU22 child", "", "", "", steer.LaunchRequest{
		TargetAgentID: h.agent.ID, Task: "WU22 child instruction",
	}))
	assert.Equal(t, []providers.Message{{Role: "user", Content: "WU22 child instruction"}}, h.store.GetHistory(h.sid))
	meta, err := h.store.GetMeta(h.sid)
	require.NoError(t, err)
	assert.Equal(t, "WU22 child", meta.Title)
	entries, err := h.store.ReadTranscript(h.sid)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, h.sid+"-task", entries[0].ID)
	assert.Equal(t, "user", entries[0].Role)
	assert.Equal(t, "WU22 child instruction", entries[0].Content)
	assert.Equal(t, h.agent.ID, entries[0].AgentID)
	assert.False(t, errors.Is(err, steer.ErrStoreWrite))
	h.assertSiblingWritable(t)
}
