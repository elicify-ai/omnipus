package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Oracle: N6 (security re-verification) — normal completion of an external-CLI
// TASK run releases the retained driver, like a delegate's completion does, so
// the last prompt and the environment snapshot (RunOptions held by the driver)
// are not retained for the life of the process. The holder itself and its
// `started` marker may stay.
// Real: processTaskDirectExternalCLI -> runExternalCLISubTurn on a recording
// driver that completes immediately; the instrument is the holder's driver field.
func TestN6_TaskCompletionReleasesTheExternalDriver(t *testing.T) {
	al, agent, _ := u10bTaskFixture(t)
	drv, restore := withRecordingDriver(t)
	defer restore()

	_, err := al.processTaskDirectExternalCLI(context.Background(), agent, u10bTaskInput, "task-key", u10bTaskChat, 0)
	require.NoError(t, err)
	runs, _ := drv.snapshot()
	require.Equal(t, 1, runs, "the task run must actually have executed (instrument check)")

	sess := al.externalRunSessionIfPresent(u10bTaskChat)
	require.NotNil(t, sess, "the run created a holder (instrument check: there was something to release)")
	sess.mu.Lock()
	defer sess.mu.Unlock()
	require.Nil(t, sess.driver, "a completed task episode must not retain its driver, prompt or environment")
	require.False(t, sess.running)
}
