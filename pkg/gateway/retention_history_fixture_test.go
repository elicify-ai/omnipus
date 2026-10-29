package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

const retentionGatewayPrivateBody = "RETENTION_HISTORY_PRIVATE_CANARY_1470"
const retentionGatewayUnavailableLog = "chat history: derived index unavailable"

type retentionGatewayFixture struct {
	store *session.UnifiedStore
	sid   string
}

func retentionGatewaySeed(t *testing.T, store *session.UnifiedStore) *retentionGatewayFixture {
	t.Helper()
	meta, err := store.NewSession(session.SessionTypeChat, "", "mia")
	require.NoError(t, err)
	_, err = store.AppendTranscriptAddressed(meta.ID, retentionGatewayEntry("retention-fresh-first"))
	require.NoError(t, err)
	require.NoError(t, store.CheckChatHistoryReadiness(t.Context(), meta.ID), "fixture starts with real healthy published history")
	return &retentionGatewayFixture{store: store, sid: meta.ID}
}

func retentionGatewayEntry(id string) session.TranscriptEntry {
	return session.TranscriptEntry{
		ID: id, Role: "user", Content: retentionGatewayPrivateBody, AgentID: "mia",
		ViewMembership: session.ViewMembershipChat, Timestamp: time.Now().UTC(),
	}
}

func (f *retentionGatewayFixture) breakIndex(t *testing.T, reason string) {
	t.Helper()
	format := filepath.Join(f.indexDir(), "FORMAT")
	switch reason {
	case "healthy":
		return
	case "catchup_limit":
		before := retentionGatewayFiles(t, f.indexDir())
		canonicalBefore := retentionGatewayCanonical(t, f)
		// A real accepted/fsynced crash suffix, not a fake reconciliation error:
		// the standalone canonical writer has no derived-publication callback.
		archive, err := session.NewArchiveDayStore(f.store.BaseDir(), f.sid)
		require.NoError(t, err)
		for i := 0; i < 65; i++ { // online recovery accepts at most64 facts;65 refuses
			_, err := archive.Append(session.ArchiveRecord{TranscriptEntry: retentionGatewayEntry(fmt.Sprintf("retention-gap-%03d", i))})
			require.NoError(t, err)
		}
		require.Equal(t, before, retentionGatewayFiles(t, f.indexDir()), "positive control: accepted suffix has NOT been indexed")
		require.NotEqual(t, canonicalBefore, retentionGatewayCanonical(t, f), "positive control: real canonical suffix exists")
	case "index_inconsistent":
		require.NoError(t, os.WriteFile(format, []byte("owned-retention-incompatible-format\n"), 0o600))
	case "index_missing":
		require.NoError(t, os.Remove(format)) // keep the index directory discoverable by boot/sweep
	case "storage_error":
		require.NoError(t, os.Remove(format))
		require.NoError(t, os.Mkdir(format, 0o700)) // real ReadFile error, independent of root permissions
	default:
		t.Fatalf("unsupported owned retention fixture %q", reason)
	}
}

func retentionGatewayReopenedFixture(t *testing.T, reason string) *retentionGatewayFixture {
	t.Helper()
	home := t.TempDir()
	base := filepath.Join(home, "sessions")
	store, err := session.NewUnifiedStoreWithHome(base, home)
	require.NoError(t, err)
	f := retentionGatewaySeed(t, store)
	t.Cleanup(func() { require.NoError(t, f.store.Close()) })
	// Join the first store's stats flusher BEFORE planting crash residue. The
	// closed store is used only for its path; it is never swept/read as a live
	// store. Reopening exercises the REAL non-healing constructor boot pass.
	require.NoError(t, store.Close())
	f.breakIndex(t, reason)
	index := retentionGatewayFiles(t, f.indexDir())
	canonical := retentionGatewayCanonical(t, f)
	reopened, err := session.NewUnifiedStoreWithHome(base, home)
	require.NoError(t, err)
	f.store = reopened
	require.Equal(t, index, retentionGatewayFiles(t, f.indexDir()), "boot must not heal this index or rebuild a missing marker")
	require.Equal(t, canonical, retentionGatewayCanonical(t, f), "boot must preserve accepted content")
	return f
}

func (f *retentionGatewayFixture) indexDir() string {
	return filepath.Join(f.store.BaseDir(), f.sid, session.ChatHistoryIndexDirectory)
}

type retentionGatewayFileFact struct { // not-wire-format: owned filesystem snapshot only.
	Bytes string
	Mode  fs.FileMode
	Mtime int64
}

func retentionGatewayFiles(t *testing.T, root string) map[string]retentionGatewayFileFact {
	t.Helper()
	out := make(map[string]retentionGatewayFileFact)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = retentionGatewayFileFact{Bytes: string(data), Mode: info.Mode().Perm(), Mtime: info.ModTime().UnixNano()}
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, out, "positive control: snapshot sees the real checkpoint/generation files even when FORMAT is unavailable")
	return out
}

func retentionGatewayCanonical(t *testing.T, f *retentionGatewayFixture) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(f.store.BaseDir(), f.sid, "*.jsonl"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	out := make(map[string][]byte)
	for _, path := range paths {
		out[filepath.Base(path)], err = os.ReadFile(path)
		require.NoError(t, err)
	}
	return out
}

func retentionGatewayAgedSession(t *testing.T, store *session.UnifiedStore) string {
	t.Helper()
	meta, err := store.NewSession(session.SessionTypeChat, "", "mia")
	require.NoError(t, err)
	_, err = store.AppendTranscriptAddressed(meta.ID, retentionGatewayEntry("retention-aged-first"))
	require.NoError(t, err)
	path := filepath.Join(store.BaseDir(), meta.ID, "transcript.jsonl")
	aged := time.Now().Add(-30 * 24 * time.Hour) // well beyond the configured7-day window
	require.NoError(t, os.Chtimes(path, aged, aged))
	require.FileExists(t, path, "positive control: an actual aged session transcript exists")
	return filepath.Dir(path)
}

func retentionGatewayCapture(t *testing.T) *syncLogBuffer {
	t.Helper()
	logs := captureSlogWarn(t)
	slog.Warn("retention isolation log capture control", "control", true)
	control := retentionGatewayEvents(t, logs, "retention isolation log capture control")
	require.Len(t, control, 1, "empty warning/error assertions require a working capture")
	require.Equal(t, "WARN", control[0]["level"])
	require.Equal(t, true, control[0]["control"])
	return logs
}

func retentionGatewayEvents(t *testing.T, logs *syncLogBuffer, message string) []map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(logs.String()))
	decoder.UseNumber()
	var events []map[string]any
	for {
		var event map[string]any
		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			return events
		}
		require.NoError(t, err, "all captured slog records must be valid JSON, never silently skipped")
		if event["msg"] == message {
			events = append(events, event)
		}
	}
}

func retentionGatewayRequireP3(t *testing.T, logs *syncLogBuffer, f *retentionGatewayFixture, reason string) {
	t.Helper()
	warnings := retentionGatewayEvents(t, logs, retentionGatewayUnavailableLog)
	if reason == "healthy" {
		require.Empty(t, warnings)
	} else {
		require.Len(t, warnings, 1, "one existing P3 warning per unchanged failure episode, including boot's warning")
		require.Equal(t, "WARN", warnings[0]["level"])
		require.Equal(t, f.sid, warnings[0]["session_id"])
		require.Equal(t, reason, warnings[0]["reason"])
		require.Contains(t, warnings[0], "cause")
	}
	require.Empty(t, retentionGatewayEvents(t, logs, "session: chat history index rebuilt"), "boot and sweep cannot heal")
	require.Empty(t, retentionGatewayEvents(t, logs, "session: chat history index rebuild failed"), "even an unsuccessful heal attempt is forbidden")
	require.NotContains(t, logs.String(), retentionGatewayPrivateBody)
}
