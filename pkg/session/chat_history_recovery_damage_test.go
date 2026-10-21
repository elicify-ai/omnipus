package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Identify actual append-grown derived files at mutation_written. No private
// log filename, codec or guessed generation ID is a test dependency.
func TestChatHistoryRecovery_TornUnpublishedTailVersusFramedDamage(t *testing.T) {
	for _, tc := range []struct {
		torn  bool
		crash string
	}{{true, ""}, {false, ""}, {true, "before_torn_tail_trim"}, {true, "torn_tail_trimmed"}} {
		torn := tc.torn
		name := "complete_bad_derived_row_refused"
		if torn {
			name = "torn_unpublished_derived_tail_redone"
		}
		if tc.crash != "" {
			name = tc.crash
		}
		t.Run(name, func(t *testing.T) {
			s, sid := newEffectsSession(t)
			historyRows(t, s, sid, "base", 1)
			historyState(t, s, sid)
			root := historyIndexDir(s, sid)
			before := historyTree(t, root)
			captured := map[string]historyFileFact{}
			old := chatHistoryPublicationHook
			chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
				if e.SessionID == sid && e.Point == "mutation_written" {
					captured = historyTree(t, root)
					return errors.New("crash after actual derived mutation write")
				}
				return nil
			}
			_, err := s.AppendTranscriptAddressed(sid, TranscriptEntry{ID: "durable-damage-fixture", Role: "user", Content: "saved", AgentID: "mia"})
			require.NoError(t, err)
			chatHistoryPublicationHook = old
			t.Cleanup(func() { chatHistoryPublicationHook = old })
			require.NotEmpty(t, captured, "real writer must reach mutation_written")
			planted := 0
			for path, after := range captured {
				prior := before[path].Bytes
				growth := len(after.Bytes) - len(prior)
				if growth <= 2 || !strings.HasPrefix(after.Bytes, prior) || filepath.Base(path) == "FORMAT" || filepath.Base(path) == "checkpoint.json" {
					continue
				}
				full := filepath.Join(root, path)
				if torn {
					require.NoError(t, os.Truncate(full, int64(len(prior)+growth/2)))
				} else {
					b := []byte(after.Bytes)
					b[len(prior)+growth/2] ^= 0x7f
					require.NoError(t, os.WriteFile(full, b, after.Mode))
				}
				planted++
			}
			require.Positive(t, planted, "instrument must alter actual newly appended derived bytes, not an unused guessed file")
			if tc.crash != "" {
				require.NoError(t, s.Close())
				historyRunCrash(t, s.BaseDir(), sid, tc.crash, 1, 86)
				reopened, e := NewUnifiedStore(s.BaseDir())
				require.NoError(t, e)
				t.Cleanup(func() { require.NoError(t, reopened.Close()) })
				s = reopened
			}
			canonical := partitionBytes(t, s, sid)
			_, err = s.reconcileChatHistoryIndex(context.Background(), sid)
			if torn {
				require.NoError(t, err, "provably torn unpublished derived tail may be redone within64")
				require.Equal(t, []string{"base-000001", "durable-damage-fixture"}, historyIDs(historyRead(t, s, sid, historyOptions(40))))
			} else {
				require.ErrorIs(t, err, ErrChatHistoryUnavailable, "framed corruption is not a trim-and-forget tail")
			}
			require.Equal(t, canonical, partitionBytes(t, s, sid), "canonical bytes never trimmed by derived recovery")
		})
	}
}

func TestChatHistoryRecovery_PrivateModelRefAdvancesCoverageNotRows(t *testing.T) {
	s, sid := newEffectsSession(t)
	historyRows(t, s, sid, "base", 1)
	historyState(t, s, sid)
	a, err := s.archiveStore(sid)
	require.NoError(t, err)
	source, err := a.Append(payloadRecord(t, "saved-source", time.Now().UTC().Format("2006-01-02"), "display source"))
	require.NoError(t, err)
	_, err = PlaceModelRef(a, source)
	require.NoError(t, err, "real body-free canonical placement")
	r, err := s.reconcileChatHistoryIndex(context.Background(), sid)
	require.NoError(t, err)
	require.Equal(t, 2, r.ArchiveRecords)
	require.Equal(t, 2, r.AppliedItems)
	require.Equal(t, []string{"base-000001", "saved-source"}, historyIDs(historyRead(t, s, sid, historyOptions(40))), "model_ref adds no chronological public source row")
}

func TestChatHistoryRecovery_AppendBeyondCapturedSuffixRemainsNextCut(t *testing.T) {
	s, sid := newEffectsSession(t)
	historyRows(t, s, sid, "base", 1)
	historyState(t, s, sid)
	historyUnpublishedArchive(t, s, sid, 1)
	opened, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once, unblock sync.Once
	old := chatHistoryReadHook
	chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
		if e.SessionID == sid && e.Scope == "recovery" && e.Phase == "record_opened" && e.Purpose == "suffix" {
			once.Do(func() {
				close(opened)
				select {
				case <-release:
				case <-ctx.Done():
				}
			})
		}
		return nil
	}
	var result chatHistoryReconcileResult
	var reconcileErr error
	go func() {
		defer close(done)
		result, reconcileErr = s.reconcileChatHistoryIndex(context.Background(), sid)
	}()
	t.Cleanup(func() {
		unblock.Do(func() { close(release) })
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("captured-suffix recovery did not join")
		}
		chatHistoryReadHook = old
	})
	select {
	case <-opened:
	case <-done:
		require.NoError(t, reconcileErr, "recovery must enter actual suffix I/O")
		t.Fatal("suffix read never opened")
	case <-time.After(10 * time.Second):
		t.Fatal("suffix-open barrier never reached")
	}
	writeDone := make(chan error, 1)
	go func() {
		_, err := s.AppendTranscriptAddressed(sid, TranscriptEntry{ID: "beyond-captured-end", Role: "user", Content: "later", AgentID: "mia"})
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		require.NoError(t, err, "canonical append cannot wait for slow recovery suffix content I/O")
	case <-time.After(10 * time.Second):
		unblock.Do(func() { close(release) })
		<-writeDone
		t.Fatal("recovery held append hostage")
	}
	unblock.Do(func() { close(release) })
	<-done
	require.NoError(t, reconcileErr)
	require.Equal(t, 1, result.ObservedItems, "must not chase the moving live tail")
	_, err := s.reconcileChatHistoryIndex(context.Background(), sid)
	require.NoError(t, err)
	require.Equal(t, []string{"base-000001", "suffix-000", "beyond-captured-end"}, historyIDs(historyRead(t, s, sid, historyOptions(40))))
}

func TestChatHistoryRecovery_FreshIdentityRequiresDurableEmptyCheckpoint(t *testing.T) {
	for _, point := range []string{"empty_checkpoint_ready", "before_identity_publish"} {
		t.Run(point, func(t *testing.T) {
			s := newTestStore(t)
			old := chatHistoryPublicationHook
			calls := 0
			chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
				if e.Point == point {
					calls++
					return errors.New("injected empty-index publication failure")
				}
				return nil
			}
			t.Cleanup(func() { chatHistoryPublicationHook = old })
			_, err := s.NewSession(SessionTypeChat, "webchat", "mia")
			require.Error(t, err, "creation must refuse when a healthy empty checkpoint cannot precede identity")
			require.Equal(t, 1, calls, "common creation path must call the real publication hook")
			metas, err := s.ListSessions()
			require.NoError(t, err)
			require.Empty(t, metas, "no fake readable empty identity")
		})
	}
}
