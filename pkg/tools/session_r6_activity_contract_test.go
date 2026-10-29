package tools

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder Q2, authorized API-only/full proof clones. This file intentionally
// uses the named read-only API; it is not run against unmodified a523, where
// that new method does not exist. No production file is changed by QA.
func TestProcessSessionR6ActivityContract(t *testing.T) {
	t.Run("InitialAndUnchangedRunningPollsDoNotFabricateProgress", func(t *testing.T) {
		ps := &ProcessSession{Status: StatusRunning, outputBuffer: &bytes.Buffer{}}
		require.True(t, ps.LastActivityAt().IsZero())
		for i := 0; i < 3; i++ {
			assert.Equal(t, "running", ps.GetStatus())
			ps.ToSessionInfo()
			ps.SetStatus(StatusRunning)
		}
		assert.True(t, ps.LastActivityAt().IsZero(), "polling and an unchanged running status are not activity")
	})
	t.Run("ActualSetupPipeWritesStampWhileStillRunning", func(t *testing.T) {
		ps := &ProcessSession{Status: StatusRunning, outputBuffer: &bytes.Buffer{}}
		readSetupSessionPipe(ps, strings.NewReader("first stdout bytes"))
		first := ps.LastActivityAt()
		require.False(t, first.IsZero(), "the REAL existing pipe reader, not direct test appendOutput, must stamp activity")
		require.Equal(t, "running", ps.GetStatus(), "completion must not mask missing output stamping")
		readSetupSessionPipe(ps, strings.NewReader("; second stderr bytes"))
		assert.True(t, ps.LastActivityAt().After(first), "a second real pipe write advances activity before completion")
		ps.mu.Lock()
		buffered := ps.outputBuffer.String()
		ps.mu.Unlock()
		assert.Equal(t, "first stdout bytes; second stderr bytes", buffered)
	})
	t.Run("ReadOnlyGetterNeverDrainsOrChangesActivity", func(t *testing.T) {
		ps := &ProcessSession{Status: StatusRunning, outputBuffer: &bytes.Buffer{}}
		readSetupSessionPipe(ps, strings.NewReader("still available to the actual output consumer"))
		first := ps.LastActivityAt()
		require.False(t, first.IsZero())
		for i := 0; i < 5; i++ {
			assert.Equal(t, first, ps.LastActivityAt())
		}
		ps.mu.Lock()
		buffered := ps.outputBuffer.String()
		ps.mu.Unlock()
		assert.Equal(t, "still available to the actual output consumer", buffered, "never invoke ProcessSession.Read to detect activity")
		assert.Equal(t, first, ps.LastActivityAt())
	})
	t.Run("EmptyPipeReadDoesNotAdvanceClock", func(t *testing.T) {
		ps := &ProcessSession{Status: StatusRunning, outputBuffer: &bytes.Buffer{}}
		readSetupSessionPipe(ps, strings.NewReader("actual earlier output"))
		before := ps.LastActivityAt()
		require.False(t, before.IsZero())
		readSetupSessionPipe(ps, strings.NewReader(""))
		assert.Equal(t, before, ps.LastActivityAt())
	})
	t.Run("ExistingTerminalSetterStampsOnce", func(t *testing.T) {
		ps := &ProcessSession{Status: StatusRunning, outputBuffer: &bytes.Buffer{}}
		ps.SetStatus(StatusDone)
		completed := ps.LastActivityAt()
		require.False(t, completed.IsZero(), "real terminal transition is progress even with no output")
		assert.Equal(t, "done", ps.GetStatus())
		ps.SetStatus(StatusDone)
		ps.GetStatus()
		assert.Equal(t, completed, ps.LastActivityAt(), "unchanged terminal reads/sets are not new completions")
	})
	t.Run("SuccessfulActualKillWriterStampsButFailedKillDoesNot", func(t *testing.T) {
		previous := killProcessGroupFn
		defer func() { killProcessGroupFn = previous }()
		wantedPID := 45219
		killProcessGroupFn = func(pid int) error { require.Equal(t, wantedPID, pid); return nil } // OS process edge only
		ps := &ProcessSession{PID: wantedPID, Status: StatusRunning, outputBuffer: &bytes.Buffer{}}
		require.NoError(t, ps.KillAndRelabel(StatusCanceled))
		assert.Equal(t, "canceled", ps.GetStatus())
		assert.False(t, ps.LastActivityAt().IsZero(), "actual successful terminal writer must stamp progress")
		failed := &ProcessSession{PID: wantedPID, Status: StatusRunning, outputBuffer: &bytes.Buffer{}}
		denied := errors.New("fixture OS kill denied")
		killProcessGroupFn = func(int) error { return denied }
		require.ErrorIs(t, failed.KillAndRelabel(StatusCanceled), denied)
		assert.Equal(t, "running", failed.GetStatus())
		assert.True(t, failed.LastActivityAt().IsZero(), "failed kill cannot fabricate a completion")
	})
	t.Run("RealSetupCompletionWriterStamps", func(t *testing.T) {
		ps := &ProcessSession{Status: StatusRunning, outputBuffer: &bytes.Buffer{}}
		tool := &EnvironmentSetupTool{}
		tool.persistCompletionOutcome(ps, setupPublication{outcome: "committed", generation: "r6-generation", dir: "/fixture"}, "completed output", true, 0)
		assert.Equal(t, "done", ps.GetStatus())
		assert.Equal(t, 0, ps.GetExitCode())
		assert.False(t, ps.LastActivityAt().IsZero(), "existing setup completion writer is covered, not only SetStatus")
		ps.mu.Lock()
		buffered := ps.outputBuffer.String()
		ps.mu.Unlock()
		assert.Equal(t, "completed output", buffered)
	})
}
