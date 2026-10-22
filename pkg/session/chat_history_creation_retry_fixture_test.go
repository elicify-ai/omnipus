package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const gate0CreationChildEnv = "OMNIPUS_TEST_GATE0_MAIN_CREATION_CRASH" // test executable only
const gate0CreationWorkspace = "gate0-create-retry"
const gate0CreationAgent = "mia"

func gate0CreationCrashChild(t *testing.T) {
	t.Helper()
	root, point := os.Getenv("OMNIPUS_TEST_GATE0_MAIN_ROOT"), os.Getenv(gate0CreationChildEnv)
	require.True(t, filepath.IsAbs(root), "explicit owned child fixture root, never a default/live home")
	store, err := NewUnifiedStore(root)
	require.NoError(t, err)
	sid, err := MainSessionID(gate0CreationWorkspace, gate0CreationAgent)
	require.NoError(t, err)
	chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
		if e.SessionID == sid && e.Point == point {
			fmt.Printf("GATE0_MAIN_CREATION_CRASH sid=%s point=%s\n", sid, point)
			os.Exit(86) // real process termination after the actual durable boundary
		}
		return nil
	}
	_, err = store.GetOrCreateMainSession(gate0CreationWorkspace, gate0CreationAgent)
	t.Fatalf("F2 real creation failed to reach crash boundary; err=%v", err)
}

func gate0CreationIncomplete(t *testing.T, point string, crash bool) (string, string) {
	t.Helper()
	root := t.TempDir()
	sid, err := MainSessionID(gate0CreationWorkspace, gate0CreationAgent)
	require.NoError(t, err)
	if crash {
		executable, exeErr := os.Executable()
		require.NoError(t, exeErr)
		cmd := exec.Command(executable, "-test.run=^TestChatHistoryCreation_InterruptedMainCreationResumes$", "-test.v")
		cmd.Env = append(os.Environ(), gate0CreationChildEnv+"="+point, "OMNIPUS_TEST_GATE0_MAIN_ROOT="+root)
		body, runErr := cmd.CombinedOutput() // actual Wait/reap, not a sent-signal flag
		var exit *exec.ExitError
		require.ErrorAs(t, runErr, &exit)
		require.Equal(t, 86, exit.ExitCode(), "real child crash boundary; output=%s", body)
		require.Contains(t, string(body), "GATE0_MAIN_CREATION_CRASH sid="+sid+" point="+point)
		t.Logf("actual creation child reaped exit86 point=%s", point)
	} else {
		store, createErr := NewUnifiedStore(root)
		require.NoError(t, createErr)
		old := chatHistoryPublicationHook
		injected := errors.New("F2 controlled identity publication failure")
		observed := false
		chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
			if e.SessionID == sid && e.Point == point {
				observed = true
				return injected
			}
			return nil
		}
		t.Cleanup(func() { chatHistoryPublicationHook = old })
		_, createErr = store.GetOrCreateMainSession(gate0CreationWorkspace, gate0CreationAgent)
		chatHistoryPublicationHook = old
		require.True(t, observed, "real creation reached requested publication boundary")
		require.ErrorIs(t, createErr, injected)
		require.NoError(t, store.Close())
	}
	dir := filepath.Join(root, sid)
	_, err = os.Stat(filepath.Join(dir, "meta.json"))
	require.ErrorIs(t, err, os.ErrNotExist, "instrument: identity is truly unpublished")
	cp, err := historyLoadCheckpoint(context.Background(), dir, sid)
	require.NoError(t, err, "actual durable empty baseline survives the interrupted attempt")
	require.Zero(t, cp.Root)
	require.Zero(t, cp.Rank)
	require.Empty(t, cp.ArchiveEnds)
	require.Nil(t, cp.Anchor)
	body, err := os.ReadFile(filepath.Join(dir, archiveCurrentFile))
	require.NoError(t, err)
	require.Empty(t, body)
	return root, sid
}
