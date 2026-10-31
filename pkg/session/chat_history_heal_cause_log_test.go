package session

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 6a gate fix round (silent H1): the heal failure warning names its cause for
// every class, sanitized. The cause is mechanism text only: no path, no file
// name, no record content (selfHealRequirePrivateLogs).
func TestChatHistoryHeal_FailureWarningCarriesSanitizedCause(t *testing.T) {
	t.Run("storage_error_names_the_os_error_without_its_path", func(t *testing.T) {
		logs := selfHealCaptureLogs(t)
		f := newSelfHealFixture(t)
		private := filepath.Join(f.store.BaseDir(), f.sid, archiveCurrentFile)
		old := chatHistoryReadHook
		chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
			if old != nil {
				if err := old(ctx, e); err != nil {
					return err
				}
			}
			if e.SessionID == f.sid && e.Purpose == "suffix" && e.Phase == "record_opened" && e.Address != nil && e.Address.ByteOffset == 0 {
				// A real os error shape: the private path is in the error text.
				return &fs.PathError{Op: "open", Path: private, Err: fs.ErrPermission}
			}
			return nil
		}
		t.Cleanup(func() { chatHistoryReadHook = old })
		selfHealRequireUnavailable(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "catchup_limit")
		records := selfHealEvents(t, logs, f.sid, selfHealBadLog)
		require.Len(t, records, 1)
		selfHealRequireLogBase(t, records[0], f.sid, 1)
		require.Equal(t, "storage_error", records[0]["class"])
		require.Equal(t, "open <path>: permission denied", records[0]["cause"], "the operator sees the real errno, not only the class")
		require.NotContains(t, records[0]["cause"], f.sid, "the session folder is part of the scrubbed path")
		selfHealRequirePrivateLogs(t, logs, f)
	})
	t.Run("deadline_during_build_names_the_deadline", func(t *testing.T) {
		logs := selfHealCaptureLogs(t)
		f := newSelfHealFixture(t)
		old := f.store.historyHealWithTimeout
		t.Cleanup(func() { f.store.historyHealWithTimeout = old })
		f.store.historyHealWithTimeout = func(parent context.Context, _ time.Duration) (context.Context, context.CancelFunc) {
			return context.WithDeadline(parent, time.Time{}) // already expired: the real timeout edge
		}
		selfHealRequireUnavailable(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "catchup_limit")
		records := selfHealEvents(t, logs, f.sid, selfHealBadLog)
		require.Len(t, records, 1)
		selfHealRequireLogBase(t, records[0], f.sid, 1)
		require.Equal(t, "deadline", records[0]["class"])
		require.Contains(t, records[0]["cause"], "deadline exceeded", "the deadline branch keeps the build error")
		selfHealRequirePrivateLogs(t, logs, f)
	})
	t.Run("busy_names_the_held_slot", func(t *testing.T) {
		logs := selfHealCaptureLogs(t)
		f := newSelfHealFixture(t)
		release, _ := selfHealTransientFault(t, f, "busy")
		defer release()
		selfHealRequireUnavailable(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "catchup_limit")
		records := selfHealEvents(t, logs, f.sid, selfHealBadLog)
		require.Len(t, records, 1)
		selfHealRequireLogBase(t, records[0], f.sid, 1)
		require.Equal(t, "busy", records[0]["class"])
		require.Contains(t, records[0]["cause"], "rebuild slot")
	})
}
