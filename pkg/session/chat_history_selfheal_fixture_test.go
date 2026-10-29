package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// Oracles: selfheal-design.md §§1–5 and ROOT-CAUSE.md §§2–3,6, supplied in
// the #1470 brief. In particular, 213 unindexed records + the saved first
// record = 214 records rebuilt. No live/customer data is used here.
const (
	selfHealBacklog = 213
	selfHealCap     = int64(32 * 1024 * 1024) // design §2: strictly greater is refused
	selfHealBody    = "SELFHEAL_PRIVATE_BODY_CANARY_1470"
	selfHealOKLog   = "session: chat history index rebuilt"
	selfHealBadLog  = "session: chat history index rebuild failed"
)

type selfHealFixture struct {
	store     *UnifiedStore
	sid       string
	addresses []ArchiveAddress
}

func selfHealSeed(t *testing.T) *selfHealFixture {
	t.Helper()
	s, sid := newEffectsSession(t)
	// No periodic stats write may manufacture source_changed in a deterministic
	// rebuild fixture. Close still joins/flushes via the existing store cleanup.
	s.stopStatsFlusher()
	historyStampEmptySessionDay(t, s, sid, historyEarlierDay)
	historyArchiveDay(t, s, sid, historyLaterDay)
	first, err := s.AppendTranscriptAddressed(sid, historyUserEntry("selfheal-first", selfHealBody, 0))
	require.NoError(t, err)
	require.Equal(t, historyLaterDay, first.PartitionKey)
	info, err := os.Stat(filepath.Join(s.BaseDir(), sid, historyEarlierDay+".jsonl"))
	require.NoError(t, err, "the first next-day append must really roll yesterday's empty transcript")
	require.Zero(t, info.Size(), "the earlier day carries no record address")
	cp := historyCheckpointOf(t, s, sid)
	require.Equal(t, historyLaterDay, cp.CurrentKey)
	require.NotContains(t, cp.ArchiveEnds, historyEarlierDay)
	return &selfHealFixture{store: s, sid: sid, addresses: []ArchiveAddress{first}}
}

func selfHealLeaveLag(t *testing.T, f *selfHealFixture) {
	t.Helper()
	before := historyCheckpointOf(t, f.store, f.sid)
	old := chatHistoryPublicationHook
	injected := 0
	chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
		if e.SessionID == f.sid && e.Operation == "append" && e.Point == "canonical_synced" {
			injected++
			return errors.New("self-heal fixture: derived publication unavailable")
		}
		if old != nil {
			return old(e)
		}
		return nil
	}
	defer func() { chatHistoryPublicationHook = old }()
	owner := f.store.historyOwner(f.sid)
	// Hold the REAL owner, just as an in-flight index operation does. The
	// canonical writer must use TryLock and leave its accepted suffix alone;
	// this avoids generating hundreds of unrelated unpublished index footers.
	func() {
		owner.mu.Lock()
		defer owner.mu.Unlock()
		for i := 0; i < selfHealBacklog-1; i++ {
			a, err := f.store.AppendTranscriptAddressed(f.sid, historyUserEntry(fmt.Sprintf("selfheal-behind-%03d", i), selfHealBody, i+1))
			require.NoError(t, err, "a busy derived owner must not reject a saved canonical record")
			f.addresses = append(f.addresses, a)
		}
	}()
	// The final append runs with the owner available: its actual publication
	// failure latches owner.failure without changing canonical acceptance.
	i := selfHealBacklog - 1
	a, err := f.store.AppendTranscriptAddressed(f.sid, historyUserEntry(fmt.Sprintf("selfheal-behind-%03d", i), selfHealBody, i+1))
	require.NoError(t, err, "derived failure must not reject a saved canonical record")
	f.addresses = append(f.addresses, a)
	require.Equal(t, selfHealBacklog, injected, "positive control: every real append reached the unavailable derived-publication hook")
	after := historyCheckpointOf(t, f.store, f.sid)
	require.Equal(t, before, after, "all 213 accepted records, not merely the last 65, must remain unindexed")
	require.Equal(t, f.addresses[1].ByteOffset, after.ArchiveEnds[historyLaterDay], "only the first record is covered")
	owner.mu.Lock()
	failure := owner.failure
	owner.mu.Unlock()
	require.Error(t, failure, "the fixture must exercise an owner whose incremental path failed")
}

func newSelfHealFixture(t *testing.T) *selfHealFixture {
	t.Helper()
	f := selfHealSeed(t)
	selfHealLeaveLag(t, f)
	// The design explicitly preserves this non-healing bounded engine for boot
	// and retention. This is both a fixture check and a trigger-specific control.
	_, err := f.store.reconcileChatHistoryIndex(context.Background(), f.sid)
	selfHealRequireUnavailable(t, err, "catchup_limit")
	require.Len(t, f.addresses, 1+selfHealBacklog)
	return f
}

func selfHealRequireUnavailable(t testing.TB, err error, reason string) {
	t.Helper()
	require.IsType(t, &ChatHistoryUnavailableError{}, err, "public unavailability must keep its specific error type")
	historyRequireUnavailable(t, err, reason)
}

func selfHealCorruptTail(t testing.TB, f *selfHealFixture) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(f.store.BaseDir(), f.sid, archiveCurrentFile), os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	// Malformed but newline-framed; it is after ALL 213 valid suffix records,
	// hence beyond the 65th-record overflow probe, not a failed initial trigger.
	_, writeErr := file.WriteString("{\"id\": " + selfHealBody + "}\n")
	require.NoError(t, errors.Join(writeErr, file.Sync(), file.Close()))
}

func selfHealObserveBuilds(t testing.TB, sid string) *atomic.Int64 {
	t.Helper()
	calls := new(atomic.Int64)
	old := chatHistoryReadHook
	chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
		// Real full-build archive I/O starts at offset 0 as a suffix read.
		// Bounded reconcile starts AFTER the indexed first row; its validation
		// of that row is purpose=anchor and therefore does not count as a build.
		if e.SessionID == sid && e.Stream == "archive" && e.Purpose == "suffix" && e.Phase == "record_opened" && e.Address != nil && e.Address.ByteOffset == 0 {
			calls.Add(1)
		}
		if old != nil {
			return old(ctx, e)
		}
		return nil
	}
	t.Cleanup(func() { chatHistoryReadHook = old })
	return calls
}

func selfHealRequireIndexed(t testing.TB, f *selfHealFixture) {
	t.Helper()
	cp := historyCheckpointOf(t, f.store, f.sid)
	info, err := os.Stat(filepath.Join(f.store.BaseDir(), f.sid, archiveCurrentFile))
	require.NoError(t, err)
	require.Equal(t, map[string]int64{historyEarlierDay: 0, historyLaterDay: info.Size()}, cp.ArchiveEnds,
		"both catalog keys and the entire canonical suffix must be covered")
	tree, err := historyOpenTree(context.Background(), filepath.Join(f.store.BaseDir(), f.sid), cp, false)
	require.NoError(t, err)
	defer func() { require.NoError(t, tree.file.Close()) }()
	var addresses []ArchiveAddress
	err = tree.walk(func(n historyNode) error {
		if !strings.HasPrefix(n.Key, "r/") {
			return nil
		}
		var record historyRecord
		if decodeErr := json.Unmarshal(n.Value, &record); decodeErr != nil {
			return decodeErr
		}
		addresses = append(addresses, record.Address)
		return nil
	})
	require.NoError(t, err)
	require.Len(t, addresses, len(f.addresses), "index address count must equal the count of real canonical appends")
	require.ElementsMatch(t, f.addresses, addresses, "no append may be lost, duplicated or assigned a different address")
}

func selfHealRequirePages(t testing.TB, f *selfHealFixture) {
	t.Helper()
	opts := historyOptions(100) // existing page API cap, not the rebuild size cap
	for end := len(f.addresses); end > 0; {
		page, err := f.store.ReadChatHistoryPage(context.Background(), f.sid, opts)
		require.NoError(t, err, "a demand page read must heal before selecting, not require a separate readiness call")
		start := max(0, end-opts.Limit)
		want := make([]string, end-start)
		for i, a := range f.addresses[start:end] {
			want[i] = a.EntryID
		}
		require.Equal(t, want, historyIDs(page), "page contents/order come from the accepted input sequence")
		for i, entry := range page.Entries {
			require.Equal(t, selfHealBody, entry.Entry.Content)
			require.Equal(t, historyExpectedPosition(f.sid, f.addresses[start+i]), entry.Position)
		}
		require.Equal(t, start > 0, page.HasEarlier)
		if start > 0 {
			require.Equal(t, page.Entries[0].Position, page.NextBefore)
		} else {
			require.Empty(t, page.NextBefore)
		}
		opts.Before = page.NextBefore
		end = start
	}
}

// A thread-safe writer keeps real slog serialization while allowing inspection
// during a blocked reader/healer. Neither logs nor clocks replace the unit.
type selfHealLogs struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *selfHealLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *selfHealLogs) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func selfHealCaptureLogs(t testing.TB) *selfHealLogs {
	t.Helper()
	l := new(selfHealLogs)
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(l, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(old) })
	slog.Warn("selfheal logger positive control", "control", true)
	records := selfHealLogRecords(t, l)
	require.Len(t, records, 1, "the warning-capture instrument must see a real warning before an empty result is trusted")
	require.Equal(t, "WARN", records[0]["level"])
	require.Equal(t, true, records[0]["control"])
	return l
}

func selfHealLogRecords(t testing.TB, l *selfHealLogs) []map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(l.text()))
	decoder.UseNumber()
	var records []map[string]any
	for {
		var record map[string]any
		err := decoder.Decode(&record)
		if errors.Is(err, io.EOF) {
			return records
		}
		require.NoError(t, err, "structured warning records must be valid JSON")
		records = append(records, record)
	}
}

func selfHealEvents(t testing.TB, l *selfHealLogs, sid, message string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, record := range selfHealLogRecords(t, l) {
		if record["session_id"] == sid && record["msg"] == message {
			out = append(out, record)
		}
	}
	return out
}

func selfHealLogNumber(t testing.TB, record map[string]any, key string) int64 {
	t.Helper()
	require.IsType(t, json.Number(""), record[key], "warning field %s must be numeric", key)
	number, ok := record[key].(json.Number)
	require.True(t, ok, "warning field %s must be numeric", key)
	value, err := number.Int64()
	require.NoError(t, err, "warning field %s must be an integer", key)
	return value
}

func selfHealRequireLogBase(t testing.TB, record map[string]any, sid string, attempt int64) {
	t.Helper()
	require.Equal(t, "WARN", record["level"], "healing must be visible when Info logs are disabled")
	require.Equal(t, sid, record["session_id"])
	require.Equal(t, "catchup_limit", record["trigger"], "the structured trigger is the reason for self-heal")
	require.Equal(t, attempt, selfHealLogNumber(t, record, "attempt"))
	require.GreaterOrEqual(t, selfHealLogNumber(t, record, "duration_ms"), int64(0))
	require.NotContains(t, record, "cause", "unwrapped errors may contain private home paths")
}

func selfHealRequirePrivateLogs(t testing.TB, l *selfHealLogs, f *selfHealFixture) {
	t.Helper()
	require.NotContains(t, l.text(), selfHealBody, "no canonical content may leak into warning logs")
	for _, record := range selfHealLogRecords(t, l) {
		if record["msg"] != selfHealOKLog && record["msg"] != selfHealBadLog {
			continue
		}
		encoded, err := json.Marshal(record)
		require.NoError(t, err)
		for _, private := range []string{f.store.BaseDir(), "transcript.jsonl", "checkpoint.json"} {
			require.NotContains(t, string(encoded), private, "heal warnings expose classes, never private source paths or file names")
		}
	}
}

// Hashes plus file metadata let byte-identity assertions cover even the 32 MiB
// boundary fixture without materializing enormous strings in a failure report.
type selfHealFileProof struct {
	Digest [sha256.Size]byte
	Size   int64
	Mode   fs.FileMode
	Mtime  int64
}

func selfHealFiles(t testing.TB, root string, excludeIndex bool) map[string]selfHealFileProof {
	t.Helper()
	files := make(map[string]selfHealFileProof)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if excludeIndex && d.IsDir() && path == historyIndexPath(root) {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, file)
		if joinErr := errors.Join(copyErr, file.Close()); joinErr != nil {
			return joinErr
		}
		var digest [sha256.Size]byte
		copy(digest[:], h.Sum(nil))
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[rel] = selfHealFileProof{Digest: digest, Size: info.Size(), Mode: info.Mode().Perm(), Mtime: info.ModTime().UnixNano()}
		return nil
	})
	require.NoError(t, err)
	return files
}

func selfHealSourceSize(t testing.TB, f *selfHealFixture) int64 {
	t.Helper()
	var total int64
	for _, file := range selfHealFiles(t, filepath.Join(f.store.BaseDir(), f.sid), true) {
		total += file.Size
	}
	return total
}

func selfHealPadFile(t testing.TB, path string, size int64) {
	t.Helper()
	require.GreaterOrEqual(t, size, int64(0))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	require.NoError(t, err)
	require.NoError(t, errors.Join(file.Truncate(size), file.Close()))
}
