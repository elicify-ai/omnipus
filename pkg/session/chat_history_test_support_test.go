package session

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A controlled dependency of the real core selector, NOT a gateway byte meter.
// The two carrier contributions are explicit fixture weights. T32 separately
// tests the real generated gateway encodings and prepared projector.
func historyFixtureWeigher(_ context.Context, p ChatHistoryPage) (ChatHistoryWeight, error) {
	n := int64(len(p.Entries))
	return ChatHistoryWeight{RESTBytes: 256 + n*64, WSBytes: 384 + n*128, ProjectedBytes: 384 + n*128}, nil
}

func historyOptions(limit int) ChatHistoryPageOptions {
	return ChatHistoryPageOptions{Limit: limit, Weigh: historyFixtureWeigher}
}

func historyRead(t testing.TB, s *UnifiedStore, sid string, opts ChatHistoryPageOptions) ChatHistoryPage {
	t.Helper()
	p, err := s.ReadChatHistoryPage(context.Background(), sid, opts)
	require.NoError(t, err, "real ReadChatHistoryPage must implement the agreed runtime contract")
	require.Equal(t, sid, p.SessionID)
	require.NotNil(t, p.Entries)
	return p
}

func historyIDs(p ChatHistoryPage) []string {
	ids := make([]string, 0, len(p.Entries))
	for _, e := range p.Entries {
		ids = append(ids, e.Entry.ID)
	}
	return ids
}

func historyExpectedPosition(sid string, a ArchiveAddress) HistoryPosition {
	h := sha256.New()
	for _, v := range []string{sid, a.PartitionKey, strconv.FormatInt(a.ByteOffset, 10), a.EntryID} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len([]byte(v))))
		_, _ = h.Write(n[:])
		_, _ = h.Write([]byte(v))
	}
	return HistoryPosition("hp1_" + base64.RawURLEncoding.EncodeToString(h.Sum(nil)))
}

func historyRows(t testing.TB, s *UnifiedStore, sid, prefix string, n int) []ArchiveAddress {
	t.Helper()
	out := make([]ArchiveAddress, 0, n)
	for i := 1; i <= n; i++ {
		a, err := s.AppendTranscriptAddressed(sid, TranscriptEntry{ID: fmt.Sprintf("%s-%06d", prefix, i), Role: "user", Content: "row " + strconv.Itoa(i), AgentID: "mia", Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)})
		require.NoError(t, err)
		out = append(out, a)
	}
	return out
}

func historyState(t testing.TB, s *UnifiedStore, sid string) chatHistoryIndexState {
	t.Helper()
	state, err := s.readChatHistoryIndexState(context.Background(), sid)
	require.NoError(t, err, "published index state accessor must be operational, not an unimplemented declaration")
	require.Equal(t, 1, state.FormatVersion)
	require.NotEmpty(t, state.IndexGeneration)
	require.NotEmpty(t, state.RetentionGeneration)
	return state
}

type historyFileFact struct { // not-wire-format: test-only real filesystem oracle.
	Bytes string
	Mode  fs.FileMode
	Mtime int64
}

func historyTree(t testing.TB, root string) map[string]historyFileFact {
	t.Helper()
	out := map[string]historyFileFact{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) && path == root {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = historyFileFact{string(b), info.Mode().Perm(), info.ModTime().UnixNano()}
		return nil
	})
	require.NoError(t, err)
	return out
}

func historyIndexDir(s *UnifiedStore, sid string) string {
	return filepath.Join(s.BaseDir(), sid, ChatHistoryIndexDirectory)
}

// Deliberate canonical-only crash residue: real archive append+fsync bypasses
// ONLY derived publication, never replaces the reducer or encodes an index.
func historyUnpublishedArchive(t testing.TB, s *UnifiedStore, sid string, n int) []ArchiveAddress {
	t.Helper()
	a, err := s.archiveStore(sid)
	require.NoError(t, err)
	out := make([]ArchiveAddress, 0, n)
	for i := 0; i < n; i++ {
		address, err := a.Append(ArchiveRecord{TranscriptEntry: TranscriptEntry{ID: fmt.Sprintf("suffix-%03d", i), Role: "user", Content: "durable suffix", AgentID: "mia", ViewMembership: ViewMembershipChat, Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}})
		require.NoError(t, err)
		out = append(out, address)
	}
	return out
}

type historyReadRecorder struct { // not-wire-format: private actual-I/O observation.
	mu     sync.Mutex
	events []chatHistoryReadEvent
}

func (r *historyReadRecorder) hook(_ context.Context, e chatHistoryReadEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}
func (r *historyReadRecorder) snapshot() []chatHistoryReadEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]chatHistoryReadEvent(nil), r.events...)
}
func historyObserve(t testing.TB, r *historyReadRecorder) {
	t.Helper()
	old := chatHistoryReadHook
	chatHistoryReadHook = r.hook
	t.Cleanup(func() { chatHistoryReadHook = old })
}

func historyAssertSuffixReads(t testing.TB, events []chatHistoryReadEvent, allowed []ArchiveAddress) {
	t.Helper()
	known := map[ArchiveAddress]bool{}
	for _, a := range allowed {
		known[a] = true
	}
	anchors, overflow := 0, 0
	for _, e := range events {
		if e.Scope != "recovery" || e.Phase != "record_read" {
			continue
		}
		switch e.Purpose {
		case "anchor":
			anchors++
		case "overflow":
			overflow++
		case "suffix":
			if e.Stream == "archive" {
				require.NotNil(t, e.Address)
				require.True(t, known[*e.Address], "recovery decoded an omitted prefix record: %+v", e.Address)
			}
		}
	}
	require.LessOrEqual(t, anchors, 1, "one bounded saved-anchor validation")
	require.LessOrEqual(t, overflow, 1, "one65th-item overflow probe, not a lifetime recount")
}

func historyAssertNoRichExpiredMetadata(t testing.TB, s *UnifiedStore, sid string, canaries ...string) {
	t.Helper()
	for path, f := range historyTree(t, historyIndexDir(s, sid)) {
		for _, secret := range canaries {
			require.NotContains(t, f.Bytes, secret, "expired rich metadata survived under %s", path)
		}
	}
}

func historyRequireUnavailable(t testing.TB, err error, reason string) {
	t.Helper()
	require.ErrorIs(t, err, ErrChatHistoryUnavailable)
	var unavailable *ChatHistoryUnavailableError
	require.ErrorAs(t, err, &unavailable)
	require.Equal(t, reason, unavailable.Reason)
	for _, private := range []string{"/", "\\", "suffix-", "private-body"} {
		require.NotContains(t, err.Error(), private, "public error is sanitized")
	}
}

func historyDayRows(day, prefix string, n int, old *time.Time) chatHistoryFixtureDay {
	rows := make([]TranscriptEntry, n)
	for i := range rows {
		rows[i] = TranscriptEntry{ID: fmt.Sprintf("%s-%03d", prefix, i+1), Role: "user", Content: strings.Repeat("x", 64), AgentID: "mia", Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
	}
	return chatHistoryFixtureDay{Day: day, Entries: rows, MTime: old}
}
