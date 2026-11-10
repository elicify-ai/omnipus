package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Binding B is secret-only. A real ordinary inbox-backed wake still owes BOTH
// generic writes; failure of either must leave the input pending and run no body.
func TestWU4P0BOrdinaryInboxWakeRetainsMarkerAndAckRequirements(t *testing.T) {
	for _, fault := range []string{"healthy", "marker", "ack"} {
		t.Run(fault, func(t *testing.T) {
			f := newSecretWakeAcceptanceFixture(t, false, false)
			var diagnostics []string
			defer wu4P0CaptureResumeLogs(t, f, &diagnostics)()
			inboxDir := t.TempDir()
			inbox := session.NewMessageInboxStore(inboxDir)
			f.al.SetSessionMessagingStores(inbox, f.al.GetSessionLifecycleStore())
			child := f.launch(t, "p0b-ordinary-"+fault)
			messageID := appendWakeInboxEntry(t, f.al, child.SessionID, "ordinary public inbox wake")
			pending, _, _, err := inbox.Drain(child.SessionID, child.SessionID, "", 0)
			require.NoError(t, err)
			count := len(pending)
			require.Equal(t, 1, count, "instrument: ordinary input is a real unacknowledged durable inbox message")
			wake := wakeMessage(child.SessionID, messageID, child.Generation)
			wake.AsyncOriginAgentID = "omnipus"
			path := filepath.Join(inboxDir, child.SessionID+".jsonl")
			if fault == "marker" {
				path = filepath.Join(f.al.GetSessionStore().BaseDir(), child.SessionID, "transcript.jsonl")
			}
			backup := path + ".p0-ordinary-backup"
			armed := false
			restore := func() {
				if armed {
					require.NoError(t, os.Remove(path))
					require.NoError(t, os.Rename(backup, path))
					armed = false
				}
			}
			t.Cleanup(restore)
			previous := turnRegisteredTestHook
			if fault != "healthy" {
				turnRegisteredTestHook = func(id string, _ *turnState) {
					if id != child.SessionID {
						return
					}
					require.False(t, armed)
					require.NoError(t, os.Rename(path, backup))
					require.NoError(t, os.Mkdir(path, 0o700))
					armed = true
				}
				t.Cleanup(func() { turnRegisteredTestHook = previous })
			}
			err = wu4P0ProcessWake(t, f, wake, &diagnostics)
			wasArmed := armed
			restore()
			remaining, _, _, readErr := inbox.Drain(child.SessionID, child.SessionID, "", 0)
			require.NoError(t, readErr)
			count = len(remaining)
			if fault == "healthy" {
				require.NoError(t, err)
				require.Len(t, f.provider.calls(child.SessionID), 1, "healthy ordinary wake must execute its real body")
				require.Zero(t, count, "ordinary input must actually be acknowledged")
			} else {
				require.True(t, wasArmed, "instrument: ordinary refusal must reach the selected I/O cut")
				require.Error(t, err, "ordinary marker/Ack publication failure remains visible")
				require.Empty(t, f.provider.calls(child.SessionID), "B must not run an ordinary body after its marker/Ack failure")
				require.Equal(t, 1, count, "the failed ordinary input must remain unacknowledged")
			}
			entries, transcriptErr := f.al.GetSessionStore().ReadTranscript(child.SessionID)
			require.NoError(t, transcriptErr)
			markers := 0
			for _, entry := range entries {
				if entry.ID == "consumed-"+messageID {
					markers++
					require.Equal(t, "consumed "+messageID, entry.Content)
				}
			}
			wantMarkers := 1 // Healthy delivery and a failed later Ack retain the already-written marker.
			if fault == "marker" {
				wantMarkers = 0
			}
			require.Equal(t, wantMarkers, markers)
		})
	}
}
