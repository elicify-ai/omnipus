package session

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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

// #1523: a damaged frame LENGTH in an unpublished derived tail is framed
// damage, not a torn tail. The captured failing case flipped byte 5 of a frame
// header (length byte 1, value 0x00) with ^0x7f: the declared size grew by
// 0x7f0000, past the end of the file but under the frame bound, so the reader
// saw io.ErrUnexpectedEOF and trimmed a complete, checksummed transaction.
// Each case alters one exact byte located by an independent header walk; the
// torn controls prove a genuinely incomplete final frame is still redone.
func TestChatHistoryRecovery_DamagedFrameLengthIsNotTornTail(t *testing.T) {
	for _, tc := range []struct {
		name    string
		refused bool
	}{
		{"first_node_length_byte1", true},
		{"commit_length_byte1", true},
		{"commit_length_past_eof_low_bytes", true},
		{"partial_header_damaged_magic", true},
		{"torn_commit_body_damaged_json", true},
		{"torn_partial_header_control", false},
		{"torn_commit_body_control", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, sid, full, prior, body := historyUnpublishedTailFixture(t)
			frames := historyWalkFrameStarts(t, body, prior)
			require.GreaterOrEqual(t, len(frames), 2, "real writer appends node frames and one commit footer")
			commit := frames[len(frames)-1]
			switch tc.name {
			case "first_node_length_byte1":
				require.Zero(t, body[frames[0]+5], "captured failing byte value")
				body[frames[0]+5] ^= 0x7f
			case "commit_length_byte1":
				require.Zero(t, body[commit+5], "captured failing byte value")
				body[commit+5] ^= 0x7f
			case "commit_length_past_eof_low_bytes":
				// Declared size one byte past the physical end: the smallest
				// over-long length, still read as incomplete before the fix.
				size := binary.BigEndian.Uint32(body[commit+4 : commit+8])
				binary.BigEndian.PutUint32(body[commit+4:commit+8], size+1)
			case "partial_header_damaged_magic":
				body = body[:commit+20]
				body[commit+1] ^= 0x7f
			case "torn_commit_body_damaged_json":
				// Incomplete body whose bytes no compact writer prefix holds.
				body = body[:len(body)-1]
				require.Equal(t, byte('{'), body[commit+40])
				body[commit+40] ^= 0x7f
			case "torn_partial_header_control":
				body = body[:commit+20]
			case "torn_commit_body_control":
				body = body[:len(body)-1]
			}
			require.NoError(t, os.WriteFile(full, body, 0o600))
			canonical := partitionBytes(t, s, sid)
			_, err := s.reconcileChatHistoryIndex(context.Background(), sid)
			if tc.refused {
				require.ErrorIs(t, err, ErrChatHistoryUnavailable, "framed corruption is not a trim-and-forget tail")
				var unavailable *ChatHistoryUnavailableError
				require.ErrorAs(t, err, &unavailable)
				require.Equal(t, "index_inconsistent", unavailable.Reason)
				after, readErr := os.ReadFile(full)
				require.NoError(t, readErr)
				require.Equal(t, body, after, "damaged derived bytes stay on disk for diagnosis")
			} else {
				require.NoError(t, err, "a genuinely torn unpublished final frame is redone")
				require.Equal(t, []string{"base-000001", "durable-damage-fixture"}, historyIDs(historyRead(t, s, sid, historyOptions(40))))
			}
			require.Equal(t, canonical, partitionBytes(t, s, sid), "canonical bytes never trimmed by derived recovery")
		})
	}
}

// historyUnpublishedTailFixture crashes the real writer at mutation_written
// and returns the one derived file that grew, its published end and its bytes.
func historyUnpublishedTailFixture(t *testing.T) (*UnifiedStore, string, string, int, []byte) {
	t.Helper()
	s, sid := newEffectsSession(t)
	historyRows(t, s, sid, "base", 1)
	historyState(t, s, sid)
	root := historyIndexDir(s, sid)
	before := historyTree(t, root)
	captured := map[string]historyFileFact{}
	old := chatHistoryPublicationHook
	t.Cleanup(func() { chatHistoryPublicationHook = old })
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
	var grown []string
	for path, after := range captured {
		prior := before[path].Bytes
		if len(after.Bytes) > len(prior) && strings.HasPrefix(after.Bytes, prior) && filepath.Base(path) != "FORMAT" && filepath.Base(path) != "checkpoint.json" {
			grown = append(grown, path)
		}
	}
	require.Len(t, grown, 1, "exactly one actual derived generation file grows")
	return s, sid, filepath.Join(root, grown[0]), len(before[grown[0]].Bytes), []byte(captured[grown[0]].Bytes)
}

// historyWalkFrameStarts is an independent instrument for the CHI1 layout read
// in chat_history_tree.go: 4-byte magic, 4-byte big-endian length, 32-byte
// SHA-256, body. It requires every appended frame to be complete and intact.
func historyWalkFrameStarts(t *testing.T, body []byte, start int) []int {
	t.Helper()
	var starts []int
	for off := start; off < len(body); {
		require.GreaterOrEqual(t, len(body)-off, 40)
		require.Equal(t, "CHI1", string(body[off:off+4]))
		next := off + 40 + int(binary.BigEndian.Uint32(body[off+4:off+8]))
		require.LessOrEqual(t, next, len(body))
		sum := sha256.Sum256(body[off+40 : next])
		require.Equal(t, sum[:], body[off+8:off+40])
		starts = append(starts, off)
		off = next
	}
	return starts
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
