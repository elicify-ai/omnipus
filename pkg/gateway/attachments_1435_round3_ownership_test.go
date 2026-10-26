package gateway

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssue1435R3_OwnerReadErrorCannotAuthorizeUploadDeletion(t *testing.T) {
	for _, state := range []string{"readable_owner_control", "absent_owner_control", "corrupt_metadata", "metadata_io_error"} {
		t.Run(state, func(t *testing.T) {
			api, original := issue1435R2UploadAPI(t)
			sessions := api.agentLoop.GetSessionStore()
			owner, err := sessions.NewSession(session.SessionTypeChat, "web", "mia")
			require.NoError(t, err)
			ownerID := owner.ID
			restore := func() {}
			if state == "absent_owner_control" {
				ownerID = "confirmed-absent-owner"
				_, lookupErr := sessions.GetMeta(ownerID)
				require.ErrorIs(t, lookupErr, fs.ErrNotExist, "control must prove confirmed owner absence")
			} else if state == "corrupt_metadata" || state == "metadata_io_error" {
				require.NoError(t, sessions.FlushAndEvictSessionMeta(ownerID))
				stats := filepath.Join(sessions.BaseDir(), ownerID, "stats.json")
				previous, readErr := os.ReadFile(stats)
				hadStats := readErr == nil
				if readErr != nil {
					require.ErrorIs(t, readErr, fs.ErrNotExist)
				}
				if state == "metadata_io_error" {
					if hadStats {
						require.NoError(t, os.Remove(stats))
					}
					require.NoError(t, os.Mkdir(stats, 0o700))
				} else {
					require.NoError(t, os.WriteFile(stats, []byte("{ definitely malformed stats json"), 0o600))
				}
				armed := true
				restore = func() {
					if !armed {
						return
					}
					require.NoError(t, os.Remove(stats))
					if hadStats {
						require.NoError(t, os.WriteFile(stats, previous, 0o600))
					}
					sessions.EvictSessionMeta(ownerID)
					armed = false
				}
				t.Cleanup(restore)
				_, lookupErr := sessions.GetMeta(ownerID)
				require.Error(t, lookupErr, "instrument: real uncached metadata read must fail")
				require.False(t, errors.Is(lookupErr, fs.ErrNotExist), "instrument: metadata failure is NOT confirmed owner absence")
				if state == "corrupt_metadata" {
					var syntax *json.SyntaxError
					require.ErrorAs(t, lookupErr, &syntax)
				} else {
					var pathErr *os.PathError
					require.ErrorAs(t, lookupErr, &pathErr)
				}
				t.Logf("real owner-read fault: %v", lookupErr)
			}
			recorder := issue1435R2Upload(t, api, ownerID)
			if recorder.Code != http.StatusCreated {
				// Visible refusal is an allowed safe outcome on a read error.
				require.True(t, recorder.Code >= 400 && recorder.Code < 600)
				var refusal gen.ErrorResponse
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &refusal))
				require.NotEmpty(t, refusal.Error, "refusal must reach the user, not only a server log")
				return
			}
			ref, path := issue1435R2AcceptedRef(t, recorder, original)
			_, metadata, err := original.ResolveWithMeta(ref)
			require.NoError(t, err)
			if state == "absent_owner_control" {
				require.Equal(t, media.CleanupPolicyDeleteOnCleanup, metadata.CleanupPolicy)
				return
			}
			assert.Equal(t, media.CleanupPolicyForgetOnly, metadata.CleanupPolicy,
				"#1435 R3 item 2: inability to read a real owner must not authorize deletion of an accepted upload")
			restore()
			_, err = sessions.GetMeta(ownerID)
			require.NoError(t, err, "owner metadata is readable again and the conversation still exists")
			aged := issue1435R2AgedStore(t, api, original, ref)
			aged.CleanExpired()
			contents, readErr := os.ReadFile(path)
			assert.NoError(t, readErr, "#1435 R3 item 2: accepted attachment bytes must survive cleanup after a temporary owner-read failure")
			assert.Equal(t, "accepted owner-scoped attachment", string(contents))
			_, resolveErr := aged.Resolve(ref)
			assert.NoError(t, resolveErr, "#1435 R3 item 2: accepted attachment ref must remain readable after metadata recovery")
		})
	}
}
