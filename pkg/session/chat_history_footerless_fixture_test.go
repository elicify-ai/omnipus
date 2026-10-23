package session

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type gate0SavedNodeTail struct {
	store      *UnifiedStore
	sid, path  string
	checkpoint historyCheckpoint
	prefix     []byte
	addresses  []ArchiveAddress
	footerAt   int64
	footer     *historyCheckpoint
}

// Actual canonical append + common reducer + node writer. The existing
// publication hook stops the real transaction, not a test-written tree.
func gate0SavedNodeTailFixture(t *testing.T, pending int) gate0SavedNodeTail {
	t.Helper()
	s, sid := newEffectsSession(t)
	base := historyRows(t, s, sid, "base", 2)
	page := historyRead(t, s, sid, historyOptions(100))
	for i, a := range base {
		require.Equal(t, historyExpectedPosition(sid, a), page.Entries[i].Position)
	}
	dir := filepath.Join(s.BaseDir(), sid)
	cp, err := historyLoadCheckpoint(context.Background(), dir, sid)
	require.NoError(t, err)
	path := filepath.Join(dir, ChatHistoryIndexDirectory, "generations", cp.File)
	prefix, err := os.ReadFile(path)
	require.NoError(t, err)
	require.EqualValues(t, cp.MutationEnd, len(prefix))
	fixture := gate0SavedNodeTail{store: s, sid: sid, path: path, checkpoint: cp, prefix: prefix, addresses: base}
	if pending > 0 {
		written := min(pending, 64) // D1a unique canonical mutations, NOT tree-node count.
		fixture.addresses = append(fixture.addresses, historyUnpublishedArchive(t, s, sid, written)...)
		old := chatHistoryPublicationHook
		injected := errors.New("F-M2 stop actual candidate after node/footer write, before checkpoint")
		calls := 0
		chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
			if e.SessionID == sid && e.Point == "mutation_written" {
				calls++
				require.Equal(t, written, e.Item)
				return injected
			}
			return nil
		}
		t.Cleanup(func() { chatHistoryPublicationHook = old })
		_, err = s.reconcileChatHistoryIndex(context.Background(), sid)
		chatHistoryPublicationHook = old
		require.ErrorIs(t, err, injected, "the actual node writer reached the publication boundary")
		require.Equal(t, 1, calls)
		after, err := historyLoadCheckpoint(context.Background(), dir, sid)
		require.NoError(t, err)
		require.Equal(t, cp, after, "old published root/coverage remains active")
		fixture.footerAt, fixture.footer = gate0InspectRealNodeTail(t, path, cp.MutationEnd)
		if pending == 65 {
			a, err := s.archiveStore(sid)
			require.NoError(t, err)
			address, err := a.Append(ArchiveRecord{TranscriptEntry: TranscriptEntry{
				ID: "suffix-064", Role: "user", Content: "durable overflow item", AgentID: "mia",
				ViewMembership: ViewMembershipChat, Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC),
			}})
			require.NoError(t, err)
			fixture.addresses = append(fixture.addresses, address)
		}
	}
	require.NoError(t, s.Close(), "join the producer before offline derived-tail filesystem faults/reopen")
	return fixture
}

// Independent framing/SHA256 instrument: it does not ask the production frame
// reader to certify its own writer. Every tail node is COMPLETE and checked;
// exactly one complete final commit is found so removal has an exact boundary.
func gate0InspectRealNodeTail(t *testing.T, path string, start int64) (int64, *historyCheckpoint) {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	nodes := 0
	for off := start; off < int64(len(body)); {
		const header = int64(40) // Actual CHI1 layout read in chat_history_tree.go; fixture instrument only.
		require.GreaterOrEqual(t, int64(len(body))-off, header)
		require.Equal(t, "CHI1", string(body[off:off+4]))
		size := int64(binary.BigEndian.Uint32(body[off+4 : off+8]))
		require.Positive(t, size)
		next := off + header + size
		require.LessOrEqual(t, next, int64(len(body)), "no physically partial frame")
		payload := body[off+header : next]
		sum := sha256.Sum256(payload)
		require.Equal(t, sum[:], body[off+8:off+header], "actual complete frame checksum independently checked")
		var frame historyFrame
		require.NoError(t, json.Unmarshal(payload, &frame))
		if frame.Commit != nil {
			require.Nil(t, frame.Node)
			require.Positive(t, nodes, "actual common tree writer produced node frames, not a footer-only fake")
			require.EqualValues(t, len(body), next, "exactly one footer, at physical tail")
			require.Equal(t, next, frame.Commit.MutationEnd)
			t.Logf("actual complete checked node frames=%d; final footer offset=%d", nodes, off)
			return off, frame.Commit
		}
		require.NotNil(t, frame.Node)
		require.NotEmpty(t, frame.Node.Key)
		nodes++
		off = next
	}
	t.Fatal("BLOCKED: actual common writer did not produce the required node/commit fixture for code-reviewer M2")
	return 0, nil
}

func gate0SetTailFault(t *testing.T, fixture gate0SavedNodeTail, kind string) {
	t.Helper()
	body, err := os.ReadFile(fixture.path)
	require.NoError(t, err)
	switch kind {
	case "footerless":
		body = body[:fixture.footerAt] // All preceding complete checksummed nodes remain; NO footer/partial frame.
	case "checksum_damage":
		body = body[:fixture.footerAt]
		body[fixture.checkpoint.MutationEnd+40] ^= 1 // Real complete node's checksum becomes invalid, never benign residue.
	case "wrong_commit_owner":
		cp := *fixture.footer
		cp.SessionID = fmt.Sprintf("%0*s", len(cp.SessionID), "x") // Same encoded length; only owner differs.
		encoded, err := historyEncodeFrame(historyFrame{Commit: &cp})
		require.NoError(t, err)
		require.Len(t, encoded, len(body)-int(fixture.footerAt))
		body = append(body[:fixture.footerAt], encoded...)
	case "committed":
		return // Actual complete transaction remains byte-for-byte intact.
	default:
		t.Fatalf("unknown M2 tail fault %s", kind)
	}
	f, err := os.OpenFile(fixture.path, os.O_RDWR, 0)
	require.NoError(t, err)
	_, writeErr := f.WriteAt(body, 0)
	truncateErr := f.Truncate(int64(len(body)))
	syncErr := f.Sync()
	closeErr := f.Close()
	require.NoError(t, errors.Join(writeErr, truncateErr, syncErr, closeErr))
}
