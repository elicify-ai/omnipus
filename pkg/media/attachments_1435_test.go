package media

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// Oracle: issue #1435, Expected item 1. An accepted upload belongs to the
// conversation, not to the cleaner's wall-clock age. The real transcript and
// registry are both reopened; only the existing store clock seam is controlled.
func TestIssue1435_ConversationAttachmentResolvesAfterRestartAndCleanup(t *testing.T) {
	const cleanupAge = time.Hour // Fixture policy, not a product default.
	for _, tc := range []struct {
		name string
		age  time.Duration
	}{
		{"before_cleanup_age", cleanupAge - time.Nanosecond},
		{"at_cleanup_age", cleanupAge},
		{"after_cleanup_age", cleanupAge + time.Nanosecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			conversationDir := filepath.Join(home, "sessions")
			conversations, err := session.NewUnifiedStore(conversationDir)
			require.NoError(t, err)
			closed := false
			t.Cleanup(func() {
				if !closed {
					require.NoError(t, conversations.Close())
				}
			})
			conversation, err := conversations.NewSession(session.SessionTypeChat, "web", "jarvis")
			require.NoError(t, err)

			uploadDir, ok := SessionUploadsDir(conversation.ID)
			require.True(t, ok, "fixture session must have a valid uploads directory")
			require.NoError(t, os.MkdirAll(uploadDir, 0o700))
			path := filepath.Join(uploadDir, "accepted.txt")
			wantBytes := []byte("accepted attachment must survive the conversation's next turn\n")
			require.NoError(t, os.WriteFile(path, wantBytes, 0o600))
			wantMeta := MediaMeta{
				Filename:      "accepted.txt",
				ContentType:   "text/plain",
				Source:        "upload:webchat",
				CleanupPolicy: CleanupPolicyForgetOnly,
			}
			acceptedAt := time.Date(2026, time.October, 23, 9, 13, 0, 0, time.UTC)
			store := NewFileMediaStoreWithCleanup(MediaCleanerConfig{MaxAge: cleanupAge})
			store.nowFunc = func() time.Time { return acceptedAt }
			t.Cleanup(store.Stop)
			// Match the live upload scope/policy, not the tool-inline pinned scope.
			ref, err := store.Store(path, wantMeta, "upload:"+conversation.ID)
			require.NoError(t, err)
			beforePath, beforeMeta, err := store.ResolveWithMeta(ref)
			require.NoError(t, err, "positive control: accepted ref must resolve before restart")
			require.Equal(t, path, beforePath)
			require.Equal(t, wantMeta, beforeMeta)
			wantAttachments := []session.Attachment{{
				Type: "file", Path: ref, Size: int64(len(wantBytes)), MIMEType: "text/plain",
			}}
			require.NoError(t, conversations.AppendTranscript(conversation.ID, session.TranscriptEntry{
				ID: "accepted-attachment", Role: "user", Content: "Read the attachment.",
				AgentID: "jarvis", Timestamp: acceptedAt, Attachments: wantAttachments,
			}))

			// Graceful gateway restart: flush the old instance, then use fresh stores.
			store.Stop()
			require.NoError(t, conversations.Close())
			closed = true
			reopenedConversations, err := session.NewUnifiedStore(conversationDir)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopenedConversations.Close()) })
			entries, err := reopenedConversations.ReadTranscript(conversation.ID)
			require.NoError(t, err)
			require.Len(t, entries, 1, "the accepted conversation entry must survive restart")
			require.Equal(t, wantAttachments, entries[0].Attachments,
				"the conversation must retain the exact accepted ref, not a new registration")

			reopened := NewFileMediaStoreWithCleanup(MediaCleanerConfig{MaxAge: cleanupAge})
			t.Cleanup(reopened.Stop)
			require.NoError(t, reopened.LoadRegistry())
			reopened.nowFunc = func() time.Time { return acceptedAt.Add(tc.age) }
			// Drive the real cleaner deterministically; no timers or sleeps.
			reopened.CleanExpired()
			gotPath, gotMeta, err := reopened.ResolveWithMeta(entries[0].Attachments[0].Path)
			require.NoError(t, err,
				"#1435: accepted conversation attachment must remain resolvable after restart and cleanup at age %s", tc.age)
			require.Equal(t, path, gotPath)
			require.Equal(t, wantMeta, gotMeta)
			gotBytes, err := os.ReadFile(gotPath)
			require.NoError(t, err)
			require.Equal(t, wantBytes, gotBytes, "resolving a path alone is not proof the attachment stayed readable")
		})
	}
}
