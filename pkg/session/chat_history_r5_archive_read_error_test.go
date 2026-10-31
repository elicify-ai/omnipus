package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// historyR5ArchiveFile fragments actual file reads, injecting at most one
// non-EOF error inside a UTF-8 rune. Later ReadAt calls recover normally.
// A clean ReadAt coalesces the fragments: io.ReaderAt forbids a short nil-error
// return. This serial test instrument never truncates a fragment to fit p.
type historyR5ArchiveFile struct { // not-wire-format: test-only filesystem edge.
	file          *os.File
	split         int64
	fault         error
	injected      int
	fragments     int
	closes        int
	splitObserved bool
	returned      bytes.Buffer
}

func (f *historyR5ArchiveFile) ReadAt(p []byte, off int64) (int, error) {
	const fragmentBytes = 17 // Small physical fragments, not a product size limit.
	n := 0
	for n < len(p) {
		at := off + int64(n)
		if at == f.split && f.fault != nil && f.injected == 0 {
			f.injected++
			return n, f.fault
		}
		end := min(len(p), n+fragmentBytes)
		if at < f.split {
			end = min(end, int(f.split-off))
		}
		read, err := f.file.ReadAt(p[n:end], at)
		f.fragments++
		f.returned.Write(p[n : n+read])
		n += read
		if off+int64(n) == f.split {
			f.splitObserved = true
		}
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func (f *historyR5ArchiveFile) Close() error {
	f.closes++
	return f.file.Close()
}

type historyR5ArchiveReadFixture struct { // not-wire-format: real canonical fixture.
	store          *UnifiedStore
	sid, dir, path string
	raw            []byte
	want           ArchiveRecord
	record         historyRecord
	cut            ChatHistoryReadCut
	split          int64
}

func historyR5NewArchiveReadFixture(t *testing.T) historyR5ArchiveReadFixture {
	t.Helper()
	s, sid := newEffectsSession(t)
	entry := TranscriptEntry{
		ID: "r5-archive-owner", Type: EntryTypeMessage, Role: "user", AgentID: "mia", ViewMembership: ViewMembershipChat,
		Timestamp: time.Date(2026, 10, 31, 7, 0, 0, 0, time.UTC), Content: "before R5_ARCHIVE_BODY_CANARY € after",
	}
	address, err := s.AppendTranscriptAddressed(sid, entry)
	require.NoError(t, err, "fixture uses the real canonical appender")
	require.NoError(t, s.Close(), "join background writes before replacing a global read seam")
	dir := filepath.Join(s.BaseDir(), sid)
	path, err := historyArchivePath(dir, address.PartitionKey)
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Zero(t, address.ByteOffset, "fixture has exactly one canonical record")
	require.Equal(t, 1, bytes.Count(raw, []byte{'\n'}), "one complete framed record")
	var want ArchiveRecord
	require.NoError(t, json.Unmarshal(raw, &want))
	require.Equal(t, ArchiveRecord{TranscriptEntry: entry}, want, "canonical bytes hold exactly the specified public entry")
	require.NoError(t, want.Validate())
	position := historyExpectedPosition(sid, address)
	page := historyRead(t, s, sid, historyOptions(20))
	require.Equal(t, []ChatHistoryEntry{{Position: position, Entry: entry, RowCount: 1}}, page.Entries, "uninstrumented positive control")
	sum := sha256.Sum256(raw)
	runeOffset := bytes.Index(raw, []byte("€"))
	require.GreaterOrEqual(t, runeOffset, 0, "fixture contains the three-byte rune")
	return historyR5ArchiveReadFixture{
		store: s, sid: sid, dir: dir, path: path, raw: raw, want: want, cut: page.Cut,
		record: historyRecord{Address: address, Position: position, Length: int64(len(raw)), Digest: hex.EncodeToString(sum[:])},
		split:  int64(runeOffset + 1), // Exactly between the first and second UTF-8 bytes (SEC-R5-2).
	}
}

type historyR5ArchiveProbe struct { // not-wire-format: observations of real production reads.
	files []*historyR5ArchiveFile
	reads []chatHistoryReadEvent
}

func historyR5ProbeArchiveReads(t *testing.T, fixture historyR5ArchiveReadFixture, fault error) *historyR5ArchiveProbe {
	t.Helper()
	probe := &historyR5ArchiveProbe{}
	oldOpen, oldHook := historyStreamOpenArchive, chatHistoryReadHook
	t.Cleanup(func() { historyStreamOpenArchive, chatHistoryReadHook = oldOpen, oldHook })
	historyStreamOpenArchive = func(path string) (historyStreamFile, error) {
		require.Equal(t, fixture.path, path, "the real archive wrapper resolves the canonical partition")
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		f := &historyR5ArchiveFile{file: file, split: fixture.split, fault: fault}
		probe.files = append(probe.files, f)
		return f, nil
	}
	chatHistoryReadHook = func(ctx context.Context, event chatHistoryReadEvent) error {
		if event.Stream == "archive" && event.Scope == "page" && event.Phase == "record_read" {
			probe.reads = append(probe.reads, event)
		}
		if oldHook != nil {
			return oldHook(ctx, event)
		}
		return nil
	}
	return probe
}

// Check the instrument against bytes actually returned through the production
// wrapper, not a copied scanner/digest pipeline. Reaching record_read with the
// complete count, then the exact scanner sentinel, also excludes a short-read
// or digest-mismatch refusal masquerading as the expected SEC-R5-2 refusal.
func historyR5CheckArchiveReads(t *testing.T, fixture historyR5ArchiveReadFixture, probe *historyR5ArchiveProbe, faults int) {
	t.Helper()
	require.Len(t, probe.files, 1, "one real archive open; no whole-read fallback")
	f := probe.files[0]
	require.Equal(t, 1, f.closes, "the actual archive wrapper closes its handle")
	require.Equal(t, faults, f.injected, "the file edge emits exactly the requested number of non-EOF errors")
	require.True(t, f.splitObserved, "both controls cross the same UTF-8 byte boundary")
	require.Greater(t, f.fragments, 1, "actual file reads are fragmented")
	require.Equal(t, fixture.raw, f.returned.Bytes(), "reads after the fault recover every original byte, in order")
	sum := sha256.Sum256(f.returned.Bytes())
	require.Equal(t, fixture.record.Digest, hex.EncodeToString(sum[:]), "recovered bytes satisfy the canonical SHA-256")
	require.Len(t, probe.reads, 1, "the production archive wrapper reaches its post-drain count/digest gate")
	require.Equal(t, fixture.record.Length, probe.reads[0].Bytes, "actual production count is complete even after the error")
	require.NotNil(t, probe.reads[0].Address)
	require.Equal(t, fixture.record.Address, *probe.reads[0].Address)
	after, err := os.ReadFile(fixture.path)
	require.NoError(t, err)
	require.Equal(t, fixture.raw, after, "no canonical bytes were mutated")
	t.Logf("archive_boundary bytes=%d non_eof_errors=%d fragments=%d closes=%d digest=%s", probe.reads[0].Bytes, f.injected, f.fragments, f.closes, fixture.record.Digest)
}

// A2.8 / SEC-R5-2: a single transient non-EOF archive error is fatal even
// when the real wrapper's drain subsequently obtains every byte and digest.
// Observed mutation RED (removing the sticky-error protection) belongs to the
// independent CHECK in a scratch clone; the test author never mutates production.
func TestChatHistoryPage_R5ArchiveReadErrorFailsClosedWithCompleteDigest(t *testing.T) {
	for _, boundary := range []string{"archive", "page"} {
		t.Run(boundary, func(t *testing.T) {
			fixture := historyR5NewArchiveReadFixture(t)
			fault := errors.New("R5_IO_CAUSE_CANARY synthetic non-EOF archive error")
			probe := historyR5ProbeArchiveReads(t, fixture, fault)
			if boundary == "archive" {
				record, err := historyStreamArchive(context.Background(), fixture.dir, fixture.record, fixture.cut, "page", "selected", newHistoryStandIns())
				require.ErrorIs(t, err, errHistoryStreamUnreadable)
				require.EqualError(t, err, "history display stream: unreadable record", "content-free diagnostic, not the injected cause")
				require.Equal(t, ArchiveRecord{}, record, "refusal returns no partial canonical record")
			} else {
				page := historyRead(t, fixture.store, fixture.sid, historyOptions(20))
				want := ChatHistoryEntry{
					Position: fixture.record.Position, RowCount: 1,
					Entry: TranscriptEntry{ID: fixture.want.ID, Role: fixture.want.Role, displayUnavailable: true},
				}
				require.Equal(t, []ChatHistoryEntry{want}, page.Entries, "A2.8: exactly one D4 placeholder, never recovered or partial text")
				require.True(t, page.Entries[0].Entry.DisplayUnavailable())
				require.Equal(t, 1, page.PageInfo.SourceGroupCount)
				require.Equal(t, 1, page.PageInfo.RowCount)
				require.False(t, page.HasEarlier)
				require.False(t, page.HasLater)
			}
			historyR5CheckArchiveReads(t, fixture, probe, 1)
		})
	}
}

// The identical canonical file and filesystem seam, with clean fragments,
// must remain readable at both the archive and the actual page boundary.
func TestChatHistoryPage_R5CleanArchiveFragmentsEqualFullEntry(t *testing.T) {
	for _, boundary := range []string{"archive", "page"} {
		t.Run(boundary, func(t *testing.T) {
			fixture := historyR5NewArchiveReadFixture(t)
			probe := historyR5ProbeArchiveReads(t, fixture, nil)
			if boundary == "archive" {
				record, err := historyStreamArchive(context.Background(), fixture.dir, fixture.record, fixture.cut, "page", "selected", newHistoryStandIns())
				require.NoError(t, err)
				require.Equal(t, fixture.want, record, "clean fragments preserve the entire canonical record")
			} else {
				page := historyRead(t, fixture.store, fixture.sid, historyOptions(20))
				want := ChatHistoryEntry{Position: fixture.record.Position, Entry: fixture.want.TranscriptEntry, RowCount: 1}
				require.Equal(t, []ChatHistoryEntry{want}, page.Entries, "clean fragments preserve the exact public projection")
				require.False(t, page.Entries[0].Entry.DisplayUnavailable())
			}
			historyR5CheckArchiveReads(t, fixture, probe, 0)
		})
	}
}
