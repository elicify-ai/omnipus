package media

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssue1435R3_PinnedRefSurvivesTemporaryStatFailure(t *testing.T) {
	for _, scopePrefix := range []string{SessionUploadScopePrefix, SessionInlineScopePrefix} {
		for _, state := range []string{"healthy_control", "confirmed_missing_control", "temporary_storage_error"} {
			t.Run(scopePrefix+state, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("OMNIPUS_HOME", home)
				conversations, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, conversations.Close()) })
				owner, err := conversations.NewSession(session.SessionTypeChat, "web", "mia")
				require.NoError(t, err)
				parent := filepath.Join(home, "uploads", owner.ID)
				require.NoError(t, os.MkdirAll(parent, 0o700))
				path := filepath.Join(parent, "accepted.txt")
				want := []byte("accepted conversation attachment: preserve these exact bytes")
				require.NoError(t, os.WriteFile(path, want, 0o600))
				acceptedAt := time.Date(2026, time.October, 20, 10, 0, 0, 0, time.UTC)
				store := NewFileMediaStoreWithCleanup(MediaCleanerConfig{MaxAge: time.Hour})
				t.Cleanup(store.Stop)
				store.nowFunc = func() time.Time { return acceptedAt }
				ref, err := store.Store(path, MediaMeta{Filename: "accepted.txt", ContentType: "text/plain", CleanupPolicy: CleanupPolicyForgetOnly}, scopePrefix+owner.ID)
				require.NoError(t, err)
				store.nowFunc = func() time.Time { return acceptedAt.Add(2 * time.Hour) }
				if state == "confirmed_missing_control" {
					require.NoError(t, os.Remove(path))
					require.Equal(t, 1, store.CleanExpired(), "confirmed absence releases the stale mapping")
					_, err := store.Resolve(ref)
					require.ErrorIs(t, err, ErrNotFound)
					return
				}
				restore := func() {}
				if state == "temporary_storage_error" {
					backup := parent + "-temporarily-offline"
					require.NoError(t, os.Rename(parent, backup))
					require.NoError(t, os.WriteFile(parent, []byte("temporary filesystem obstruction"), 0o600))
					armed := true
					restore = func() {
						if !armed {
							return
						}
						require.NoError(t, os.Remove(parent))
						require.NoError(t, os.Rename(backup, parent))
						armed = false
					}
					t.Cleanup(restore)
					_, statErr := os.Stat(path)
					var pathErr *os.PathError
					require.ErrorAs(t, statErr, &pathErr, "instrument: the real filesystem must reject this stat")
					require.False(t, errors.Is(statErr, fs.ErrNotExist), "instrument: this is a storage/access failure, NOT confirmed deletion")
					t.Logf("real non-absence stat fault: %v", statErr)
				}
				released := store.CleanExpired()
				restore()
				actualBytes, err := os.ReadFile(path)
				require.NoError(t, err, "fault recovery must restore real access to the accepted file")
				require.Equal(t, want, actualBytes)
				_, err = conversations.GetMeta(owner.ID)
				require.NoError(t, err, "the owning conversation remains live")
				assert.Equal(t, 0, released, "#1435 R3 item 3: a temporary non-not-found stat failure cannot authorize releasing a pinned ref")
				resolved, err := store.Resolve(ref)
				require.NoError(t, err, "#1435 R3 item 3: restoring storage access must leave the SAME accepted ref resolvable")
				require.Equal(t, path, resolved)
			})
		}
	}
}
