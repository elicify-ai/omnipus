package tools

// environment_setup_lifecycle_killrace_test.go — regression tests for the
// kill-vs-publication race in the generic environment_setup tool. The
// window is opened deterministically by blocking the fake target's Commit
// behind channel gates — the same fake seam the sibling lifecycle tests
// use — so every interleaving below is scheduled by channel handoff, never
// by timing sleeps.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -p 1 -run
// '^TestEnvironmentSetup_(KillRace|KillAction)' ./pkg/tools/

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- kill landing inside the publication window -------------------------------

// TestEnvironmentSetup_KillRace_PublicationWindow_Committed proves that a
// kill (action=kill, or identically the RequestCancel cascade / shutdown
// reaper — every one of them relabels through KillAndRelabel) landing in the
// publication window cannot mislabel an install whose shared publication
// COMMITTED. The natural successful exit is captured before publishTarget
// runs, so once Commit is entered the capture is already done; the kill
// lands while Commit is held open, then publication completes. The terminal
// record must read done / exit 0 with the committed note — not the "killed"
// label the mid-flight relabel left behind.
func TestEnvironmentSetup_KillRace_PublicationWindow_Committed(t *testing.T) {
	prefix := pathForTempSubdir(t, "prefix")
	target := &lifecycleTarget{prefix: prefix}
	// Deterministic publication barrier: `entered` fires once Commit has been
	// reached (capture strictly precedes publishTarget, so the natural exit
	// is already recorded); `release` holds Commit open so the kill lands
	// inside the window.
	entered := make(chan struct{})
	release := make(chan struct{})
	target.commitFn = func() (string, string, error) {
		close(entered)
		<-release
		return "gen-kill-race", filepath.Join(prefix, "published"), nil
	}
	h := newLifecycleHarness(t, lifecycleStore{target: target})
	ctx := h.ctx("mia", "alpha", "", "t-kill-race-1")

	done := make(chan *ToolResult, 1)
	start := h.tool.ExecuteAsync(ctx, map[string]any{
		"command": `echo installed > "$OMNIPUS_ENV_PREFIX/artifact.txt"`,
		"purpose": "kill-in-publication-window probe",
		"scope":   "shared",
	}, func(_ context.Context, result *ToolResult) {
		done <- result
	})
	require.False(t, start.IsError, "start refused: %s", start.ForLLM)
	sessionID := extractSessionID(t, start.ForLLM)

	// Commit entered → natural exit already captured, session still truthfully
	// "running" (publication not settled). Land the kill inside the window.
	<-entered
	kill := h.tool.Execute(ctx, map[string]any{"action": "kill", "session_id": sessionID})
	require.NotNil(t, kill)
	require.False(t, kill.IsError, "in-window kill action failed: %s", kill.ForLLM)

	// Let publication finish; the callback fires after the terminal persist.
	close(release)
	var completion *ToolResult
	select {
	case completion = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("completion callback never fired after commit release")
	}

	// The install IS published and the command exited 0 — every surface must
	// say so. Without the persist-time correction the session would stay
	// relabeled "killed" (exit -1) while the generation committed anyway.
	require.False(t, completion.IsError, "committed publication must not error: %s", completion.ForLLM)
	assert.Contains(t, completion.ForLLM, "published: generation gen-kill-race")
	resp, res := pollAction(t, h.tool, ctx, sessionID)
	assert.Equal(t, "done", resp.Status,
		"a kill in the publication window must not mislabel a committed install")
	assert.Equal(t, 0, resp.ExitCode,
		"a committed install must keep its real exit code, not the relabel's -1")
	assert.Contains(t, res.ForLLM, "publication: committed")
}

// --- preservation: kill BEFORE the natural exit stays truthful ---------------

// TestEnvironmentSetup_KillRace_BeforeNaturalExit_Truthful pins the behavior
// that must hold alongside the publication-window correction: a kill that
// lands while the command is STILL RUNNING is a real cancellation — no
// publication, staging aborted, session killed. The terminal override is
// keyed on a COMMITTED publication, never on the mere absence of a kill, so
// genuine cancellations cannot be swallowed.
func TestEnvironmentSetup_KillRace_BeforeNaturalExit_Truthful(t *testing.T) {
	prefix := pathForTempSubdir(t, "prefix")
	tmpDir := pathForTempSubdir(t, "tmp")
	// No commitFn: had the command exited 0 naturally this target WOULD
	// commit — so commitCalls==0 below proves the kill genuinely preempted
	// publication rather than racing it.
	target := &lifecycleTarget{
		prefix: prefix,
		cache:  pathForTempSubdir(t, "cache"),
		tmp:    tmpDir,
	}
	h := newLifecycleHarness(t, lifecycleStore{target: target})
	ctx := h.ctx("mia", "alpha", "", "t-kill-race-2")

	done := make(chan *ToolResult, 1)
	start := h.tool.ExecuteAsync(ctx, map[string]any{
		"command": `echo started > "$OMNIPUS_ENV_TMP/started"; while [ ! -f "$OMNIPUS_ENV_TMP/stop" ]; do sleep 0.05; done`,
		"purpose": "kill-before-natural-exit probe",
		"scope":   "shared",
	}, func(_ context.Context, result *ToolResult) {
		done <- result
	})
	require.False(t, start.IsError, "start refused: %s", start.ForLLM)
	sessionID := extractSessionID(t, start.ForLLM)

	// The child's own started marker proves it is provably mid-loop and
	// cannot exit (the stop file does not exist yet), so the kill is
	// deterministically a cancel-BEFORE-exit.
	waitForArtifact(t, filepath.Join(tmpDir, "started"))
	kill := h.tool.Execute(ctx, map[string]any{"action": "kill", "session_id": sessionID})
	require.NotNil(t, kill)
	require.False(t, kill.IsError, "pre-exit kill action failed: %s", kill.ForLLM)

	// The process group is dead; wait for the publication outcome.
	var completion *ToolResult
	select {
	case completion = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("completion callback never fired after pre-exit kill")
	}

	// Truthful failure preserved: no publication, staging aborted, session
	// killed with the relabel's -1.
	commitCalls, abortCalls := target.calls()
	assert.Equal(t, 0, commitCalls, "a kill before the natural exit must NOT publish")
	assert.Equal(t, 1, abortCalls, "a kill before the natural exit must abort the staging area")
	require.NotNil(t, completion)
	assert.Contains(t, completion.ForLLM, "NOT published")
	resp, res := pollAction(t, h.tool, ctx, sessionID)
	assert.Equal(t, "killed", resp.Status)
	assert.Equal(t, -1, resp.ExitCode)
	assert.Contains(t, res.ForLLM, "publication: not published")
}

// --- kill on a finished session keeps the publication story ------------------

// TestEnvironmentSetup_KillAction_KeepsPublicationNote pins poll/read/kill
// parity: action=kill on an already-terminal session must surface the
// persisted publication note — a finished session must not hide a publication
// failure behind a bare {"status":"done"}.
func TestEnvironmentSetup_KillAction_KeepsPublicationNote(t *testing.T) {
	cases := []struct {
		name        string
		commitErr   error
		wantNote    string
		wantIsError bool
	}{
		{name: "commit_failed_note_not_hidden", commitErr: errors.New("disk full"), wantNote: "publication: FAILED", wantIsError: true},
		{name: "committed_note_present", wantNote: "publication: committed", wantIsError: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prefix := pathForTempSubdir(t, "prefix")
			target := &lifecycleTarget{prefix: prefix}
			if tc.commitErr != nil {
				target.commitFn = func() (string, string, error) {
					return "", "", tc.commitErr
				}
			}
			h := newLifecycleHarness(t, lifecycleStore{target: target})
			ctx := h.ctx("mia", "alpha", "", "t-kill-note")

			_, completion := h.startAndWait(t, ctx, map[string]any{
				"command": `echo installed > "$OMNIPUS_ENV_PREFIX/artifact.txt"`,
				"purpose": "kill-note parity probe",
				"scope":   "shared",
			})
			require.Equal(t, tc.wantIsError, completion.IsError, completion.ForLLM)

			// The session is terminal; action=kill must carry the same
			// publication story poll/read tell.
			sessionID := startSessionID(t, completion)
			kill := h.tool.Execute(ctx, map[string]any{"action": "kill", "session_id": sessionID})
			require.NotNil(t, kill)
			assert.Contains(t, kill.ForLLM, tc.wantNote,
				"kill on a finished session must surface the persisted publication note")
		})
	}
}
